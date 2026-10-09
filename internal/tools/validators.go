// Package tools: validate action-enum tool (Task 36).
//
// Ports six source validators behind one MCP tool ("validate"), following
// the action-enum pattern of ship_state (one tool, one "action" input that
// selects the operation):
//
//   - plan_format  -- scripts/ci/validate-plan-format.js  (PF1-PF7, PF9, PF10),
//     plus PF11-PF12 and the style (PF13) and diagram-contrast (PF14) checks
//   - plan_style   -- the PF13 style measurement alone, with a styleReport
//   - discovery    -- internal/discovery.ValidateAll       (PD1-PD16, reused as-is)
//   - pr_template  -- scripts/ci/validate-pr-template.js   (V1-V5)
//   - cost_tiers   -- scripts/ci/validate-cost-tiers.js    (DRIFT/MISSING_DOC/STALE_DOC/INHERITED, NO_COST_DOC)
//   - guardrails    -- scripts/ci/validate-guardrails.js    (per-guardrail id/description/severity)
//   - dimensions   -- internal/dimensions.Validate, plus a net-new D10
//     cross-file duplicate-name check (dimensions.Validate only checks one
//     Dimension at a time and cannot see other files).
//
// A seventh action, pr_body, was folded in later: it delegates to pr.go's
// prValidateBodyCore (PR body vs. template section-presence check), which
// used to be its own standalone pr_validate_body MCP tool. Its {OK,
// Errors []string} output is adapted into []discovery.Finding by
// validatePRBody below to match this dispatcher's uniform shape.
//
// discovery.Finding{ID, Severity, Message, Path} is reused as the single
// output shape across all six actions, per the fact sheet's Refinement
// note. Findings represent FAILED checks only -- an empty Findings slice
// means every check passed, mirroring discovery.ValidateAll's own
// documented convention.
//
// Disclosed deviations from the literal Contract (all authorized by the
// fact sheet as open implementer decisions):
//
//   - ValidateIn gained two fields beyond {action, file, final, template}:
//     Strict (cost_tiers: whether INHERITED counts as "error" or
//     "warning") and Section (guardrails: which config section to read,
//     default "plan" -- confirmed against the actual JS parseArgs default,
//     not the dispatch ruling's illustrative "execute" example).
//   - cost_tiers and guardrails Finding.Message embeds the JS "kind"/
//     "target" (skill vs agent) and guardrail id since discovery.Finding
//     has no field for either.
//   - PF5's/Notes' 5000/2000-char caps from the JS lazy-lookahead regexes
//     are not enforced (Go's RE2 has no lookahead); blocks run to the next
//     boundary marker or EOF. This only matters for pathologically long
//     Acceptance-criteria/Notes blocks, which do not occur in real plans.
//   - stripFences is a manual line-scan state machine, not a port of the
//     JS backreference regex (RE2 cannot express \1 backreferences). It
//     reproduces the same "closing fence must be >= opening fence length"
//     semantics; discovery.Finding has no Line field, so exact
//     line-position fidelity of the stripped text does not matter.
package tools

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/rnagrodzki/sdlc-plugin/internal/commstyle"
	"github.com/rnagrodzki/sdlc-plugin/internal/config"
	"github.com/rnagrodzki/sdlc-plugin/internal/dimensions"
	"github.com/rnagrodzki/sdlc-plugin/internal/discovery"
	"github.com/rnagrodzki/sdlc-plugin/internal/frontmatter"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
	"github.com/rnagrodzki/sdlc-plugin/internal/prtemplate"
	"github.com/rnagrodzki/sdlc-plugin/internal/worktree"
)

// ---------------------------------------------------------------------------
// Tool contract
// ---------------------------------------------------------------------------

// ValidateIn is the input for the "validate" tool.
type ValidateIn struct {
	// Action selects the validator: plan_format | plan_style | discovery |
	// pr_template | cost_tiers | guardrails | dimensions | pr_body |
	// ci_script_drift | worktree_anchoring.
	Action string `json:"action" jsonschema:"enum=plan_format,enum=plan_style,enum=discovery,enum=pr_template,enum=cost_tiers,enum=guardrails,enum=dimensions,enum=pr_body,enum=ci_script_drift,enum=worktree_anchoring" jsonschema_description:"Which validator to run: plan_format, plan_style, discovery, pr_template, cost_tiers, guardrails, dimensions, pr_body, ci_script_drift, or worktree_anchoring. Example: plan_style"`
	// File is the plan file for plan_format and plan_style (links_validate
	// lives in links.go).
	File string `json:"file,omitempty" jsonschema_description:"Plan file to validate. Used by the plan_format and plan_style actions. Example: ~/.claude/plans/x.md"`
	// Final requests the stricter plan_format checks (PF9/PF10), matching
	// the JS --final flag.
	Final bool `json:"final,omitempty" jsonschema_description:"Requests the stricter plan_format checks (PF9/PF10). Used by the plan_format action."`
	// Template is the plan template path: plan_format reads it for PF10's
	// required sections and PF13's measured (narrative) sections; plan_style
	// reads it for the measured sections.
	Template string `json:"template,omitempty" jsonschema_description:"Plan template path. plan_format: PF10 sections and PF13 measured sections. plan_style: measured sections. Example: .sdlc-v2/plan-template.md"`
	// Strict controls whether cost_tiers' INHERITED kind is severity
	// "error" (true) or "warning" (false), matching the JS --strict flag.
	Strict bool `json:"strict,omitempty" jsonschema_description:"Controls whether the cost_tiers action's INHERITED finding kind is reported as severity \"error\" (true) or \"warning\" (false)."`
	// Section is the config section guardrails reads its guardrails list
	// from. Defaults to "plan" when empty, matching the JS default.
	Section string `json:"section,omitempty" jsonschema_description:"Config section the guardrails action reads its guardrails list from. Defaults to \"plan\" when empty."`
	// ActiveWorktree, for the guardrails action only, swaps the main-root
	// anchor for worktree.ActiveRoot() so harden -- which writes guardrails
	// to the active worktree, not the main one -- can validate what it just
	// wrote. When the active root cannot be resolved the call fails instead
	// of falling back to the main root. Ignored by every other action.
	ActiveWorktree bool `json:"activeWorktree,omitempty" jsonschema_description:"guardrails action only: true reads the section from the ACTIVE worktree's .sdlc-v2/config.toml instead of the main worktree's. Used by harden, which writes guardrails to the active worktree. Ignored by other actions."`
	// CandidatesJSON is a JSON-encoded array of proposed guardrail entries,
	// for the guardrails action only. Each candidate is checked together with
	// the section on disk, in memory: a candidate whose id matches a disk
	// entry's id replaces that entry for the check; a candidate with a new id
	// is added and all entries are checked. A candidate that lowers the
	// severity of the disk entry it replaces (error to warning) is an error
	// finding: harden is strengthen-only. Nothing is ever written. Without
	// it, only the disk entries are checked.
	CandidatesJSON string `json:"candidatesJson,omitempty" jsonschema_description:"guardrails action only: JSON array of proposed guardrail entries, e.g. [{\"id\":\"no-ci-bypass\",\"description\":\"Plans must not skip CI.\",\"severity\":\"error\"}]. Checked together with the section on disk, in memory — a candidate's id matching a disk entry replaces it for the check, a new id is added. A candidate that lowers the severity of the disk entry it replaces (error to warning) returns an error finding. Nothing is written."`
	// Body is the PR body text to validate for the pr_body action, matching
	// the former standalone pr_validate_body tool's input.
	Body string `json:"body,omitempty" jsonschema_description:"PR body text to validate. Used by the pr_body action."`
}

// ValidateOut is the output for the "validate" tool.
type ValidateOut struct {
	Findings []discovery.Finding `json:"findings"`
	// WorktreeAnchoring is populated only by the worktree_anchoring action:
	// which worktree (main or active) the .sdlc-v2/ state directory is
	// currently anchored to (Task 4/R1 — see WorktreeAnchoringCheck).
	WorktreeAnchoring *WorktreeAnchoringCheck `json:"worktreeAnchoring,omitempty"`
	// StyleReport is populated only by the plan_style action, on every call,
	// pass or fail.
	StyleReport *StyleReport `json:"styleReport,omitempty"`
}

// StyleReport is the plan_style measurement: the style settings and numeric
// limits in effect, the per-section numbers, and every hit the PF13 and PF14
// checks read. Every list field is non-nil, so JSON carries [] and never null.
type StyleReport struct {
	Settings        map[string]string          `json:"settings"`
	Limits          commstyle.Limits           `json:"limits"`
	Sections        []commstyle.SectionMetrics `json:"sections"`
	BannedHits      []commstyle.BannedHit      `json:"bannedPhraseHits"`
	SteHits         []commstyle.SteHit         `json:"steHits"`
	DiagramContrast []commstyle.ContrastHit    `json:"diagramContrast"` // same hits as PF14
	Instructions    []string                   `json:"instructions"`    // custom plan instructions, for the handoff self-check
	Warnings        []string                   `json:"warnings"`
}

// RegisterValidateTools registers the "validate" MCP tool.
func RegisterValidateTools(s *mcpserver.Server) {
	mcpserver.Register(s, "validate",
		`Run a deterministic validator against the project.

Pass "action" to select the validator. Each action uses a subset of the input fields (unlisted fields are ignored). Returns structured findings (id, severity, message, path, fix) for FAILED checks only — an empty findings list means every check passed. "fix" carries the accepted shape inline and is set on every plan_format and plan_style failure (PF1-PF14) and on every guardrails failure (the repair step); the other actions leave it empty.

validate only reports; the calling skill step decides the next action from findings and styleReport.

- plan_format: Check a plan .md against PF1-PF7, PF11, PF12, PF13 (style limits from [style] and [planStyle]) and PF14 (Mermaid diagram color contrast, whole file), plus PF9 and PF10 when final is true. Requires file. Optional: final (adds the scorecard check PF9 and, with template, the section check PF10), template (plan template path: its required sections for PF10 and its narrative sections for PF13; omit it to skip PF10 and measure Context, Research Findings, Key Decisions and Final Shape).
- plan_style: Measure a plan against the [planStyle] limits (and the strict STE rules when writingStandard is ste). Requires file. Optional: template (narrative sections to measure). Returns findings (PF13 failures) and styleReport on every call, pass or fail; styleReport.instructions lists the custom plan instructions for the handoff self-check.
- discovery: Check the project's discovery artifacts (PD1-PD16). No inputs.
- pr_template: Check the PR template file itself (V1-V5) at its canonical or legacy path. No inputs.
- cost_tiers: Compare skill/agent model tiers against the cost-tier doc tables in docs/cost-tiers.md. Optional: strict (true reports the INHERITED finding kind as severity "error" instead of "warning"). When docs/cost-tiers.md does not exist, the check is skipped and one NO_COST_DOC warning is returned.
- guardrails: Check the guardrails list in a config section for per-guardrail id/description/severity; every finding carries a fix (the repair step). Optional: section (defaults to "plan"), activeWorktree (true reads the active worktree instead of the main worktree; an unresolvable active worktree is an infrastructure error, never a silent fallback), candidatesJson (JSON array of proposed guardrail entries checked together with the section on disk, in memory, replacing same-id disk entries; nothing is written). A candidate that lowers the severity of a disk entry with the same id returns an error finding. A section that does not exist returns no findings; candidatesJson entries are still checked.
- dimensions: Check the review-dimension files, including a cross-file duplicate-name check (D10). Reads the ACTIVE worktree, unlike most other actions (ci_script_drift also reads the active worktree). No inputs.
- pr_body: Check a PR body against the PR template's required sections. Requires body — an empty body is not rejected, it simply reports every required section as missing.
- ci_script_drift: Check the generated CI scripts against their current sources. Reads the ACTIVE worktree (falls back to main when it cannot be resolved), since scaffold_ci now writes there too. No inputs.
- worktree_anchoring: Check which worktree the .sdlc-v2/ state directory is anchored to. No inputs. Also returns worktreeAnchoring{}.`,
		mcpserver.Annotations{
			Title:      "Validate SDLC artifacts",
			ReadOnly:   true,
			Idempotent: true,
			OpenWorld:  false,
		},
		func(ctx mcpserver.Ctx, in ValidateIn) (ValidateOut, error) {
			root, err := worktree.MainRoot()
			if err != nil {
				return ValidateOut{}, &mcpserver.InfraError{Msg: fmt.Sprintf("resolve project root: %s", err.Error()), Suggestion: "Run validate from inside a git working tree (or a linked worktree) of this project; no git repository could be found from the current directory.", Cause: err}
			}
			root, err = validateRoot(root, in, worktree.ActiveRoot)
			if err != nil {
				return ValidateOut{}, err
			}
			return validate(root, in)
		},
	)
}

// validateRoot picks the root an action reads from. dimensions and
// ci_script_drift are the two actions whose target files must be read from
// the ACTIVE worktree (root rule) — ci_script_drift because scaffold_ci now
// writes there too (not the main worktree every other action anchors to);
// both fail open to mainRoot when the active root cannot be resolved, so a
// resolution error never blocks them. guardrails reads the active worktree
// only when the caller opts in via ActiveWorktree (harden, which writes
// guardrails to the active worktree and validates what it just wrote). That
// opt-in fails loud instead: silently validating the main worktree would
// report a clean result for a file nobody checked.
func validateRoot(mainRoot string, in ValidateIn, activeRoot func() (string, error)) (string, error) {
	switch {
	case in.Action == "dimensions", in.Action == "ci_script_drift":
		if r, err := activeRoot(); err == nil {
			return r, nil
		}
		return mainRoot, nil
	case in.Action == "guardrails" && in.ActiveWorktree:
		r, err := activeRoot()
		if err != nil {
			return "", &mcpserver.InfraError{
				Msg:        fmt.Sprintf("resolve active worktree for guardrails activeWorktree:true: %s", err.Error()),
				Suggestion: "Run validate from inside the active git working tree (not its .git directory), or omit activeWorktree to validate the main worktree's config, then retry.",
				Cause:      err,
			}
		}
		return r, nil
	default:
		return mainRoot, nil
	}
}

func validate(root string, in ValidateIn) (ValidateOut, error) {
	var (
		findings []discovery.Finding
		anchor   *WorktreeAnchoringCheck
		style    *StyleReport
		err      error
	)
	switch in.Action {
	case "plan_format":
		findings, err = validatePlanFormat(root, in)
	case "plan_style":
		findings, style, err = validatePlanStyle(root, in)
	case "discovery":
		findings = discovery.ValidateAll(root)
	case "pr_template":
		findings, err = validatePRTemplate(root)
	case "cost_tiers":
		findings, err = validateCostTiers(root, in)
	case "guardrails":
		findings, err = validateGuardrailsAction(root, in)
	case "dimensions":
		findings, err = validateDimensionsAction(root)
	case "pr_body":
		findings, err = validatePRBody(root, in)
	case "ci_script_drift":
		findings, err = validateCIScriptDrift(root)
	case "worktree_anchoring":
		anchor, findings, err = validateWorktreeAnchoring(root)
	default:
		return ValidateOut{}, unknownActionError("validate action", in.Action, "",
			"pass one of the valid actions: plan_format, plan_style, discovery, pr_template, cost_tiers, guardrails, dimensions, pr_body, ci_script_drift, worktree_anchoring")
	}
	if err != nil {
		return ValidateOut{}, err
	}
	// Normalize nil → empty slice so JSON serializes as [] not null,
	// per the project's "no ambiguous nulls" guardrail.
	if findings == nil {
		findings = []discovery.Finding{}
	}
	return ValidateOut{Findings: findings, WorktreeAnchoring: anchor, StyleReport: style}, nil
}

// resolvePath resolves p against root unless it is already absolute.
func resolvePath(root, p string) string {
	if p == "" || filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(root, p)
}

func containsStr(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// plan_format (PF1-PF7, PF9, PF10) -- ports scripts/ci/validate-plan-format.js
// ---------------------------------------------------------------------------

// pfCheck is one plan-format check result.
//
// message says what is wrong; when it lists several offenders it uses
// pfIssueList so each one sits on its own line. fix states the accepted shape
// inline, so the author can correct the plan without opening another file.
// Every failure path currently sets a fix — even the checks whose message
// already names the expected values (PF1, PF3, PF10, PF12), where the fix
// adds what the message leaves out, such as that a header field only counts
// when its value sits on the same line as the label. A fix never consists of
// only a pointer to a reference document; TestPlanFormatFixesAreSelfContained
// pins that, and pins an inline shape for PF2, PF6, PF7 and PF9 specifically.
// fix is empty only on a passing check, which pfFindings never emits.
type pfCheck struct {
	id      string
	status  string // "pass" | "fail"
	message string
	fix     string
}

func pfPass(id, message string) pfCheck {
	return pfCheck{id: id, status: "pass", message: message}
}

func pfFail(id, message, fix string) pfCheck {
	return pfCheck{id: id, status: "fail", message: message, fix: fix}
}

// pfIssueList renders headline plus one "- " bullet per issue, so sub-issues
// stay on their own lines instead of being joined onto a single line.
func pfIssueList(headline string, issues []string) string {
	return headline + "\n- " + strings.Join(issues, "\n- ")
}

// pfLines joins fix or message lines. Indentation inside a line is part of the
// shape being shown and is kept as written.
func pfLines(lines ...string) string {
	return strings.Join(lines, "\n")
}

type planTask struct {
	Number int
	Title  string
	Body   string
}

// readPlanFile resolves file against root and reads it. Every failure carries
// a Suggestion (guardrail mcp-error-suggestion-coverage), and the three
// failures are kept apart: no file passed, the path does not exist, and the
// path exists but cannot be read (permission denied, a directory, an I/O
// error). Reporting the last two as one "file not found" sends the caller to
// re-check a path that is already correct.
func readPlanFile(root, file, action string) (filePath, content string, err error) {
	if file == "" {
		return "", "", &mcpserver.DomainError{
			Msg:        action + ": file is required",
			Suggestion: "Pass file: the path to the plan .md file, absolute or relative to the project root.",
		}
	}
	filePath = resolvePath(root, file)
	data, rerr := os.ReadFile(filePath)
	if rerr != nil {
		return "", "", planReadError(action, "file", filePath, rerr)
	}
	return filePath, string(data), nil
}

// planReadError turns an os.ReadFile failure into the DomainError the calling
// action returns. action names the caller ("plan_format" or "plan_style", so
// the message matches the action that actually failed); what names the thing
// being read ("file" for the plan, "template" for the PF10 template) so one
// helper serves every read site. Only fs.ErrNotExist is reported as
// not-found; anything else names the read failure so the caller looks at
// permissions and the file type instead of the path.
func planReadError(action, what, path string, rerr error) *mcpserver.DomainError {
	if errors.Is(rerr, fs.ErrNotExist) {
		if what == "template" {
			return &mcpserver.DomainError{
				Msg:        fmt.Sprintf("%s: template not found: %s", action, path),
				Suggestion: "Pass template: the path to the plan template .md, or omit it to skip the PF10 section check and measure the default PF13 sections (Context, Research Findings, Key Decisions, Final Shape).",
				Cause:      rerr,
			}
		}
		return &mcpserver.DomainError{
			Msg:        fmt.Sprintf("%s: file not found: %s", action, path),
			Suggestion: "Check the path. A relative path resolves against the project root.",
			Cause:      rerr,
		}
	}
	return &mcpserver.DomainError{
		Msg:        fmt.Sprintf("%s: cannot read %s: %s: %s", action, what, path, rerr.Error()),
		Suggestion: fmt.Sprintf("The path exists but could not be read. Check that %s is a regular file (not a directory) and that this process has read permission on it.", path),
		Cause:      rerr,
	}
}

// planBlockingChecks runs the checks that apply on every plan edit (PF1-PF7,
// PF11, PF12). PF9 and PF10 are final-only and are not part of this set.
func planBlockingChecks(root, content string) []pfCheck {
	tasks := extractTasks(content)
	planTasks := loadPlanTasks(root)
	return []pfCheck{
		checkPF1(content),
		checkPF2(tasks),
		checkPF3(tasks),
		checkPF4(tasks),
		checkPF5(tasks),
		checkPF6(content),
		checkPF7(tasks),
		checkPF11(tasks, planTasks.RequiredFields),
		checkPF12(tasks, planTasks.ContractShape),
	}
}

// pfFindings converts the failed checks into findings; passing checks produce
// none, per this dispatcher's "findings are failed checks only" convention.
func pfFindings(checks []pfCheck, filePath string) []discovery.Finding {
	var findings []discovery.Finding
	for _, c := range checks {
		if c.status == "fail" {
			findings = append(findings, discovery.Finding{ID: c.id, Severity: "error", Message: c.message, Path: filePath, Fix: c.fix})
		}
	}
	return findings
}

func validatePlanFormat(root string, in ValidateIn) ([]discovery.Finding, error) {
	filePath, content, err := readPlanFile(root, in.File, "plan_format")
	if err != nil {
		return nil, err
	}

	// The template is read once, up front: PF13 measures its narrative
	// sections on every call, and PF10 (final only) checks all of them.
	var (
		templatePath string
		templateSecs []TemplateSection
	)
	if in.Template != "" {
		templatePath = resolvePath(root, in.Template)
		templateSecs, err = parseTemplateRequiredSectionsFull(templatePath)
		if err != nil {
			return nil, planReadError("plan_format", "template", templatePath, err)
		}
	}

	style, _, rep := measurePlanStyle(root, content, in.Template != "", templateSecs)

	// PF13 and PF14 run here, not in planBlockingChecks: the PostToolUse hook
	// must not run PF13, and runs PF14 on the edited text only.
	checks := planBlockingChecks(root, content)
	checks = append(checks, checkPF13(style, rep), checkPF14(content))

	if in.Final {
		checks = append(checks, checkPF9(content))
		if in.Template != "" {
			names := make([]string, 0, len(templateSecs))
			for _, s := range templateSecs {
				names = append(names, s.Name)
			}
			checks = append(checks, checkPF10(content, names, templatePath))
		}
	}

	return pfFindings(checks, filePath), nil
}

// ---------------------------------------------------------------------------
// plan_style (PF13) and diagram contrast (PF14)
// ---------------------------------------------------------------------------

// defaultMeasuredSections are the sections PF13 measures when no template is
// passed. They match the narrative sections of the shipped default template.
var defaultMeasuredSections = []string{"Context", "Research Findings", "Key Decisions", "Final Shape"}

// measuredSections returns the section names PF13 measures: the template's
// narrative sections when a template was passed (none when it marks none),
// otherwise defaultMeasuredSections.
func measuredSections(haveTemplate bool, templateSecs []TemplateSection) []string {
	if !haveTemplate {
		return defaultMeasuredSections
	}
	names := []string{}
	for _, s := range templateSecs {
		if s.Narrative {
			names = append(names, s.Name)
		}
	}
	return names
}

// measurePlanStyle loads the style from the main worktree config (fresh on
// every call) and measures content against its limits. styleErr is the
// config read error, if any; the style then falls back to the defaults.
func measurePlanStyle(root, content string, haveTemplate bool, templateSecs []TemplateSection) (style PlanStyle, styleErr string, rep commstyle.Report) {
	style, styleErr = loadPlanStyle(root)
	rep = commstyle.Measure(content, measuredSections(haveTemplate, templateSecs), style.Limits)
	return style, styleErr, rep
}

const pf13Fix = `Move mechanism (names, paths, code) into a table or code block. Split paragraphs. Remove the banned phrase. Rewrite each STE hit per the writing guide, or add a code name to technicalTerms / wrap it in backticks. Run validate({action:"plan_style"}) to see per-section numbers.`

// checkPF13 turns a style measurement into one check: one "- " line per
// failed section limit, banned-phrase hit, and STE hit.
func checkPF13(style PlanStyle, rep commstyle.Report) pfCheck {
	var issues []string
	for _, s := range rep.Sections {
		for _, f := range s.Failures {
			issues = append(issues, fmt.Sprintf("%s: %s", s.Name, f))
		}
	}
	for _, h := range rep.BannedHits {
		issues = append(issues, fmt.Sprintf("line %d: banned phrase %q", h.Line, h.Phrase))
	}
	for _, h := range rep.SteHits {
		issues = append(issues, fmt.Sprintf("line %d: STE %s: %q", h.Line, h.Rule, h.Text))
	}
	if len(issues) == 0 {
		return pfPass("PF13", "Plan style limits met")
	}
	headline := fmt.Sprintf("Plan style limits from [style] and [planStyle] not met (visualDensity=%s, audience=%s, writingStandard=%s):",
		style.VisualDensity, style.Audience, style.WritingStandard)
	return pfFail("PF13", pfIssueList(headline, issues), pf13Fix)
}

const pf14Fix = `Use classDef new fill:#1f7a3a,stroke:#0b3d1c,color:#ffffff,stroke-width:2px and classDef changed fill:#8a6d00,stroke:#4a3a00,color:#ffffff,stroke-width:2px. Any other fill needs color: with contrast 4.5:1 or more.`

// checkPF14 checks every classDef/style line inside the plan's mermaid
// fences for a readable text color.
func checkPF14(content string) pfCheck {
	return pf14FromHits(commstyle.MermaidContrast(content))
}

// pf14FromHits renders contrast hits as the PF14 check, one
// "- line <N>: <reason>: <text>" line per hit. checkPF14 (whole file) and
// DiagramContrastFindings (edited text) share it so both report one shape.
func pf14FromHits(hits []commstyle.ContrastHit) pfCheck {
	if len(hits) == 0 {
		return pfPass("PF14", "Mermaid diagram colors are readable")
	}
	issues := make([]string, 0, len(hits))
	for _, h := range hits {
		issues = append(issues, fmt.Sprintf("line %d: %s: %s", h.Line, h.Reason, h.Text))
	}
	return pfFail("PF14", pfIssueList("Mermaid diagram colors are hard to read:", issues), pf14Fix)
}

// DiagramContrastFindings checks text line by line (no mermaid fence needed)
// and returns the PF14 finding, or [] when every classDef/style line is
// readable. The PostToolUse plan hook calls it on the edited text only, so an
// edit that does not touch a classDef never blocks an older plan.
func DiagramContrastFindings(filePath, text string) []discovery.Finding {
	findings := pfFindings([]pfCheck{pf14FromHits(commstyle.ContrastHitsInLines(text))}, filePath)
	if findings == nil {
		return []discovery.Finding{}
	}
	return findings
}

// validatePlanStyle runs the plan_style action: the PF13 measurement alone,
// returned as findings plus a StyleReport on every call, pass or fail.
// PF14 hits appear in styleReport.diagramContrast, not in findings.
func validatePlanStyle(root string, in ValidateIn) ([]discovery.Finding, *StyleReport, error) {
	filePath, content, err := readPlanFile(root, in.File, "plan_style")
	if err != nil {
		return nil, nil, err
	}

	var templateSecs []TemplateSection
	if in.Template != "" {
		templatePath := resolvePath(root, in.Template)
		templateSecs, err = parseTemplateRequiredSectionsFull(templatePath)
		if err != nil {
			return nil, nil, planReadError("plan_style", "template", templatePath, err)
		}
	}

	style, styleErr, rep := measurePlanStyle(root, content, in.Template != "", templateSecs)

	warnings := append([]string{}, style.Warnings...)
	if styleErr != "" {
		warnings = append(warnings, styleErr)
	}
	instructions := style.Instructions
	if instructions == nil {
		instructions = []string{}
	}
	report := &StyleReport{
		Settings: map[string]string{
			"audience":        style.Audience,
			"writingStandard": style.WritingStandard,
			"tone":            style.Tone,
			"visualDensity":   style.VisualDensity,
			"language":        style.Language,
		},
		Limits:          style.Limits,
		Sections:        rep.Sections,
		BannedHits:      rep.BannedHits,
		SteHits:         rep.SteHits,
		DiagramContrast: commstyle.MermaidContrast(content),
		Instructions:    instructions,
		Warnings:        warnings,
	}
	return pfFindings([]pfCheck{checkPF13(style, rep)}, filePath), report, nil
}

// ValidatePlanFormatForHook is the PostToolUse hook's entry point. It reads
// the plan once and returns:
//
//   - blocking: the checks that apply on every edit (PF1-PF7, PF11, PF12).
//   - willFailAtFinal: PF9, plus PF10 when a plan template resolves. Only
//     computed when blocking is non-empty, so a plan with nothing to fix stays
//     silent instead of nagging about checks that only run at --final.
//
// Both slices reuse the one read of the plan. The only other file read is the
// template, resolved by hookPlanTemplateCandidates. A template that cannot be
// found or read skips PF10 and never fails the hook.
//
// The candidate loop falls through to the next candidate only when the
// current one does not exist. A candidate that exists but cannot be read
// (permission, a directory, an I/O error) stops the loop and skips PF10: the
// project asked for that template, so previewing PF10 against the shipped
// default would check the plan against sections the project never required.
func ValidatePlanFormatForHook(root, file string) (blocking, willFailAtFinal []discovery.Finding, err error) {
	filePath, content, err := readPlanFile(root, file, "plan_format")
	if err != nil {
		return nil, nil, err
	}

	blocking = pfFindings(planBlockingChecks(root, content), filePath)
	if len(blocking) == 0 {
		return nil, nil, nil
	}

	finalChecks := []pfCheck{checkPF9(content)}
	for _, candidate := range hookPlanTemplateCandidates(root) {
		sections, terr := parseTemplateRequiredSections(candidate)
		if terr != nil {
			if errors.Is(terr, fs.ErrNotExist) {
				continue
			}
			// The candidate is there but unreadable: skip PF10 entirely
			// rather than silently checking against a different template.
			break
		}
		finalChecks = append(finalChecks, checkPF10(content, sections, candidate))
		break
	}
	return blocking, pfFindings(finalChecks, filePath), nil
}

// hookPlanTemplateCandidates lists where the hook looks for the plan template,
// cheapest first: the project override, then the shipped default under
// CLAUDE_PLUGIN_ROOT. It does not use resolveSkillTemplate: that walks the
// whole plugin cache the first time it runs, and the hook is a fresh process
// after every plan edit, so the walk would be paid on every edit.
func hookPlanTemplateCandidates(root string) []string {
	candidates := []string{filepath.Join(root, paths.DataDir, paths.PlanTemplateFile)}
	if pluginRoot := os.Getenv("CLAUDE_PLUGIN_ROOT"); pluginRoot != "" {
		candidates = append(candidates, filepath.Join(pluginRoot, "skills", "plan", "plan-template-default.md"))
	}
	return candidates
}

var fieldMarkerCache = map[string]*regexp.Regexp{}

// extractField mirrors JS extractField: new RegExp(`\*\*${fieldName}:\*\*\s*(.+?)(?:\n|$)`).
func extractField(content, fieldName string) (string, bool) {
	re, ok := fieldMarkerCache[fieldName]
	if !ok {
		re = regexp.MustCompile(`\*\*` + regexp.QuoteMeta(fieldName) + `:\*\*\s*(.+?)(?:\n|$)`)
		fieldMarkerCache[fieldName] = re
	}
	m := re.FindStringSubmatch(content)
	if m == nil {
		return "", false
	}
	v := strings.TrimSpace(m[1])
	// The \s* above skips newlines, so an empty "**Complexity:**" takes the
	// next non-blank line as its value. When that line is itself a
	// "**Label:**" line, the field is present but empty. A plain value on
	// the next line still counts.
	if fieldLabelRe.MatchString(v) {
		return "", true
	}
	return v, true
}

var fenceOpenRe = regexp.MustCompile("^(`{3,})")

// stripFences mirrors JS stripFences's CommonMark-style fence stripping via
// a manual line-scan state machine (Go's RE2 cannot express the JS
// backreference regex ^(`{3,})[^\n]*\n[\s\S]*?^\1`*[ \t]*$).
func stripFences(content string) string {
	lines := strings.Split(content, "\n")
	var out []string
	i := 0
	for i < len(lines) {
		m := fenceOpenRe.FindStringSubmatch(lines[i])
		if m == nil {
			out = append(out, lines[i])
			i++
			continue
		}
		n := len(m[1])
		closeRe := regexp.MustCompile(fmt.Sprintf("^`{%d,}[ \\t]*$", n))
		j := i + 1
		closed := false
		for ; j < len(lines); j++ {
			if closeRe.MatchString(lines[j]) {
				closed = true
				break
			}
		}
		if closed {
			i = j + 1
		} else {
			out = append(out, lines[i])
			i++
		}
	}
	return strings.Join(out, "\n")
}

var taskHeaderRe = regexp.MustCompile(`(?m)^### Task (\d+):\s*(.+)$`)

func extractTasks(content string) []planTask {
	stripped := stripFences(content)
	locs := taskHeaderRe.FindAllStringSubmatchIndex(stripped, -1)
	var tasks []planTask
	for i, loc := range locs {
		num, _ := strconv.Atoi(stripped[loc[2]:loc[3]])
		title := strings.TrimSpace(stripped[loc[4]:loc[5]])
		start := loc[0]
		end := len(stripped)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		tasks = append(tasks, planTask{Number: num, Title: title, Body: stripped[start:end]})
	}
	return tasks
}

// pf1FieldHints is the accepted value for each header field, from
// plan-format-reference.md's "Document Header" block.
var pf1FieldHints = map[string]string{
	"Goal":         "one sentence: what this plan implements",
	"Architecture": "2-3 sentences: the overall approach and key design decisions",
	"Source":       `spec file path, or "conversation context"`,
	"Verification": "primary verification command, e.g. go test ./...",
}

// fieldLabelRe matches a value that is itself a bold label line such as
// "**Architecture:** x".
var fieldLabelRe = regexp.MustCompile(`^\*\*[^*\n]+:\*\*`)

func checkPF1(content string) pfCheck {
	fields := []string{"Goal", "Architecture", "Source", "Verification"}
	var missing []string
	for _, f := range fields {
		// extractField returns "" when the value would be the next
		// "**Label:**" line, so an empty field is caught here.
		v, ok := extractField(content, f)
		if !ok || v == "" {
			missing = append(missing, f)
		}
	}
	if len(missing) > 0 {
		// The message names the fields. The fix adds the shape to write: a
		// bold label with its value on the same line. A plain value on a
		// later line is also accepted, but the same-line form is the
		// documented one.
		fix := []string{"write each as a bold label with its value on the same line, above the first task:"}
		for _, f := range missing {
			fix = append(fix, fmt.Sprintf("  **%s:** <%s>", f, pf1FieldHints[f]))
		}
		return pfFail("PF1", fmt.Sprintf("Missing or empty header field(s): %s", strings.Join(missing, ", ")), pfLines(fix...))
	}
	return pfPass("PF1", "All header fields present")
}

// pf2Fix writes out the accepted plan shape for every PF2 failure: a missing,
// misnumbered or duplicated task heading is fixed by writing the block below.
//
// It states only what plan_format actually enforces. "No forward references"
// is a plan-authoring convention documented in plan-format-reference.md, not
// a validated rule: checkPF4 checks existence and cycles only (see pf4Fix),
// so naming it here would tell the author a forward reference fails the
// check when it does not.
var pf2Fix = pfLines(
	`every task is a "### Task N: Title" heading outside any code fence, numbered from 1 (or 0) with no gaps or repeats, followed by this block:`,
	"  **Complexity:** Trivial | Standard | Complex",
	"  **Risk:** Low | Medium | High",
	`  **Depends on:** Task X, Task Y   (or "none"; every Task N must exist and the graph must have no cycle)`,
	"  **Verify:** tests | build | lint | manual   (a scope hint may follow: tests (go test ./pkg/ -run TestFoo))",
	"  **Files:**",
	"  - Modify: `path/to/file.go` - what changes",
	"  **Contract:**   (required when Files has a Create/Modify/Test bullet; see PF7)",
	"  **Acceptance criteria:**",
	"  - [ ] a specific, verifiable criterion",
)

func checkPF2(tasks []planTask) pfCheck {
	if len(tasks) == 0 {
		return pfFail("PF2", `No tasks found: the plan has no "### Task N: Title" heading outside a code fence`, pf2Fix)
	}
	numbers := make([]int, len(tasks))
	for i, t := range tasks {
		numbers[i] = t.Number
	}
	sort.Ints(numbers)
	start := numbers[0]
	if start != 0 && start != 1 {
		return pfFail("PF2", fmt.Sprintf("Task numbering must start at 0 or 1, found: %d", start), pf2Fix)
	}
	var issues []string
	for i := 1; i < len(numbers); i++ {
		if numbers[i] == numbers[i-1] {
			issues = append(issues, fmt.Sprintf("duplicate task number: %d", numbers[i]))
		} else if numbers[i] != numbers[i-1]+1 {
			issues = append(issues, fmt.Sprintf("gap between Task %d and Task %d", numbers[i-1], numbers[i]))
		}
	}
	if len(issues) > 0 {
		return pfFail("PF2", pfIssueList("Task numbering issues:", issues), pf2Fix)
	}
	return pfPass("PF2", fmt.Sprintf("%d task(s) numbered contiguously from %d", len(tasks), start))
}

var (
	validComplexity = []string{"Trivial", "Standard", "Complex"}
	validRisk       = []string{"Low", "Medium", "High"}
	validVerify     = []string{"tests", "build", "lint", "manual"}
)

// splitVerifyValues splits a **Verify:** value on commas that sit outside
// parentheses, so a scope hint that itself contains a comma stays attached to
// its value: "tests (go test ./a/ -run A,B), build" -> two values. An
// unclosed "(" keeps the rest of the field in one value, which then fails
// validVerifyValue.
func splitVerifyValues(field string) []string {
	var values []string
	depth, start := 0, 0
	for i := 0; i < len(field); i++ {
		switch field[i] {
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		case ',':
			if depth == 0 {
				values = append(values, field[start:i])
				start = i + 1
			}
		}
	}
	return append(values, field[start:])
}

// balancedParens reports whether every "(" in s has a matching ")" after it
// and no ")" comes without one.
func balancedParens(s string) bool {
	depth := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth < 0 {
				return false
			}
		}
	}
	return depth == 0
}

// validVerifyValue reports whether v is one accepted Verify value: a word from
// validVerify, optionally followed by a scope hint in parentheses, e.g.
// "tests (go test ./internal/tools/ -run TestFoo)". The hint must be
// non-empty and its parentheses must balance, so "tests (" and "tests ()"
// fail, as does any word outside validVerify ("flaky").
func validVerifyValue(v string) bool {
	v = strings.TrimSpace(v)
	for _, word := range validVerify {
		rest, ok := strings.CutPrefix(v, word)
		if !ok {
			continue
		}
		if rest == "" {
			return true
		}
		rest = strings.TrimLeft(rest, " \t")
		if !strings.HasPrefix(rest, "(") || !strings.HasSuffix(rest, ")") {
			continue
		}
		hint := rest[1 : len(rest)-1]
		if strings.TrimSpace(hint) != "" && balancedParens(hint) {
			return true
		}
	}
	return false
}

// pf3VerifyFix and pf3DependsFix are the PF3 fix lines for the two fields whose
// accepted shape the message alone does not show. Complexity and Risk already
// list their allowed values in the message, so they add no fix line.
const (
	pf3VerifyFix  = `**Verify:** tests | build | lint | manual - comma-separate several; end any value with a scope hint in balanced parentheses, e.g. **Verify:** tests (go test ./pkg/ -run TestFoo), build`
	pf3DependsFix = `**Depends on:** Task X, Task Y   (or "none")`
)

func checkPF3(tasks []planTask) pfCheck {
	var issues []string
	var verifyBad, dependsBad bool
	for _, t := range tasks {
		prefix := fmt.Sprintf("Task %d", t.Number)

		if complexity, ok := extractField(t.Body, "Complexity"); !ok || complexity == "" {
			issues = append(issues, prefix+": missing **Complexity:** (expected: "+strings.Join(validComplexity, "|")+")")
		} else if !containsStr(validComplexity, complexity) {
			issues = append(issues, fmt.Sprintf("%s: invalid Complexity %q (expected: %s)", prefix, complexity, strings.Join(validComplexity, "|")))
		}

		if risk, ok := extractField(t.Body, "Risk"); !ok || risk == "" {
			issues = append(issues, prefix+": missing **Risk:** (expected: "+strings.Join(validRisk, "|")+")")
		} else if !containsStr(validRisk, risk) {
			issues = append(issues, fmt.Sprintf("%s: invalid Risk %q (expected: %s)", prefix, risk, strings.Join(validRisk, "|")))
		}

		if dependsOn, ok := extractField(t.Body, "Depends on"); !ok || dependsOn == "" {
			issues = append(issues, prefix+`: missing **Depends on:** (expected: Task N, Task M, or "none")`)
			dependsBad = true
		}

		verify, ok := extractField(t.Body, "Verify")
		if !ok || verify == "" {
			issues = append(issues, prefix+": missing **Verify:** (expected: "+pf3VerifyExpected()+")")
			verifyBad = true
		} else {
			var invalid []string
			for _, v := range splitVerifyValues(verify) {
				if !validVerifyValue(v) {
					invalid = append(invalid, fmt.Sprintf("%q", strings.TrimSpace(v)))
				}
			}
			if len(invalid) > 0 {
				issues = append(issues, fmt.Sprintf("%s: invalid Verify value(s): %s (expected: %s)", prefix, strings.Join(invalid, ", "), pf3VerifyExpected()))
				verifyBad = true
			}
		}
	}
	if len(issues) == 0 {
		return pfPass("PF3", "All tasks have valid metadata")
	}
	var fix []string
	if dependsBad {
		fix = append(fix, pf3DependsFix)
	}
	if verifyBad {
		fix = append(fix, pf3VerifyFix)
	}
	return pfFail("PF3", pfIssueList("Invalid or missing task metadata:", issues), pfLines(fix...))
}

// pf3VerifyExpected is the accepted Verify shape shown in PF3's messages: the
// word list plus the optional scope hint.
func pf3VerifyExpected() string {
	return strings.Join(validVerify, "|") + `, each optionally followed by a "(scope hint)"`
}

// taskRefKeywordRe locates the first "Task"/"Tasks" keyword in a **Depends
// on:** field value, marking where reference extraction starts.
var taskRefKeywordRe = regexp.MustCompile(`(?i)Tasks?\b`)

// dependsOnTokenRe matches one token immediately following the cursor in the
// remainder of a **Depends on:** field: an optional leading comma/whitespace
// separator, then a number, the word "and", or a repeated Task(s) keyword.
// Anything else - notably "(", ")", "-", "." used to introduce parenthetical
// notes or trailing prose - does not match, which ends extraction.
var dependsOnTokenRe = regexp.MustCompile(`(?i)^\s*(?:,\s*)?(\d+|and|tasks?)\b`)

// parseDependsOnRefs extracts task-number references from a plan task's
// **Depends on:** field value (e.g. "Tasks 6, 7, and 8" -> [6, 7, 8]). It is
// the single shared parser for checkPF4 (plan validation) and
// waveComputeParseDependsOn (wave scheduling), so both interpret the field
// identically.
//
// Extraction starts at the first "Task"/"Tasks" keyword and consumes only
// digits, whitespace, commas, "and", and repeated Task(s) keywords - the
// first character outside that set ends the scan. So
// "Task 2 (needs Foo from line 42)" yields only [2]: the "(" stops
// extraction before the "42" inside the parenthetical is ever considered.
// A field with no "Task"/"Tasks" keyword at all (e.g. "none") returns nil.
func parseDependsOnRefs(field string) []int {
	loc := taskRefKeywordRe.FindStringIndex(field)
	if loc == nil {
		return nil
	}
	var refs []int
	rest := field[loc[1]:]
	for {
		m := dependsOnTokenRe.FindStringSubmatch(rest)
		if m == nil {
			break
		}
		if n, err := strconv.Atoi(m[1]); err == nil {
			refs = append(refs, n)
		}
		rest = rest[len(m[0]):]
	}
	return refs
}

// pf4Fix states what PF4 accepts. It does not enforce "no forward references"
// (only existence and no cycles), so the fix does not claim it.
const pf4Fix = `**Depends on:** lists only tasks that exist, written "Task N" and separated by commas (or "none"); to break a cycle, remove one dependency from the loop`

func checkPF4(tasks []planTask) pfCheck {
	taskNumbers := map[int]bool{}
	for _, t := range tasks {
		taskNumbers[t.Number] = true
	}

	var issues []string
	depGraph := map[int][]int{}

	for _, t := range tasks {
		dependsOn, ok := extractField(t.Body, "Depends on")
		if !ok || strings.EqualFold(dependsOn, "none") || dependsOn == "" {
			depGraph[t.Number] = nil
			continue
		}
		refs := parseDependsOnRefs(dependsOn)
		for _, refNum := range refs {
			if !taskNumbers[refNum] {
				issues = append(issues, fmt.Sprintf("Task %d: depends on nonexistent Task %d", t.Number, refNum))
			}
		}
		depGraph[t.Number] = refs
	}

	visited := map[int]bool{}
	inStack := map[int]bool{}

	var hasCycle func(node int, path []int) bool
	hasCycle = func(node int, path []int) bool {
		if inStack[node] {
			cycleStart := 0
			for i, n := range path {
				if n == node {
					cycleStart = i
					break
				}
			}
			cycle := append(append([]int{}, path[cycleStart:]...), node)
			parts := make([]string, len(cycle))
			for i, n := range cycle {
				parts[i] = fmt.Sprintf("Task %d", n)
			}
			issues = append(issues, fmt.Sprintf("Circular dependency: %s", strings.Join(parts, " -> ")))
			return true
		}
		if visited[node] {
			return false
		}
		visited[node] = true
		inStack[node] = true
		for _, dep := range depGraph[node] {
			if hasCycle(dep, append(append([]int{}, path...), node)) {
				return true
			}
		}
		inStack[node] = false
		return false
	}

	var order []int
	seen := map[int]bool{}
	for _, t := range tasks {
		if !seen[t.Number] {
			seen[t.Number] = true
			order = append(order, t.Number)
		}
	}
	for _, num := range order {
		if !visited[num] {
			hasCycle(num, nil)
		}
	}

	if len(issues) > 0 {
		return pfFail("PF4", pfIssueList("Invalid task dependencies:", issues), pf4Fix)
	}
	return pfPass("PF4", "All dependencies valid, no cycles")
}

var (
	acStartRe    = regexp.MustCompile(`\*\*Acceptance criteria:\*\*\s*\n`)
	notesStartRe = regexp.MustCompile(`\*\*Notes:\*\*\s*\n`)
)

// extractDelimitedBlock finds the text after startRe's match up to the
// earliest of the given boundary markers (or end of string). This replaces
// JS's lazy-quantifier + lookahead regex (Go's RE2 has no lookahead); the
// {0,5000}/{0,2000} length caps from the JS source are not enforced (see
// package doc deviation note).
func extractDelimitedBlock(body string, startRe *regexp.Regexp, boundaries []string) (string, bool) {
	loc := startRe.FindStringIndex(body)
	if loc == nil {
		return "", false
	}
	rest := body[loc[1]:]
	end := len(rest)
	for _, b := range boundaries {
		if idx := strings.Index(rest, b); idx != -1 && idx < end {
			end = idx
		}
	}
	return rest[:end], true
}

var pf5Fix = pfLines(
	"give each task an acceptance block with at least one checkbox line:",
	"  **Acceptance criteria:**",
	"  - [ ] a specific, verifiable criterion",
	"keep **Notes:** to 5 non-blank lines or fewer",
)

func checkPF5(tasks []planTask) pfCheck {
	var issues []string
	for _, t := range tasks {
		prefix := fmt.Sprintf("Task %d", t.Number)

		block, found := extractDelimitedBlock(t.Body, acStartRe, []string{"\n### ", "\n---", "\n## "})
		if !found {
			issues = append(issues, prefix+": missing **Acceptance criteria:**")
		} else if strings.Count(block, "- [ ]") == 0 {
			issues = append(issues, prefix+`: **Acceptance criteria:** has no checkbox items (expected at least one "- [ ]")`)
		}

		notesBlock, notesFound := extractDelimitedBlock(t.Body, notesStartRe, []string{"\n**", "\n### ", "\n---", "\n## "})
		if notesFound {
			lineCount := 0
			for _, l := range strings.Split(notesBlock, "\n") {
				if strings.TrimSpace(l) != "" {
					lineCount++
				}
			}
			if lineCount > 5 {
				issues = append(issues, fmt.Sprintf("%s: **Notes:** has %d non-blank lines (max 5)", prefix, lineCount))
			}
		}
	}
	if len(issues) > 0 {
		return pfFail("PF5", pfIssueList("Invalid acceptance criteria or notes:", issues), pf5Fix)
	}
	return pfPass("PF5", "All tasks have valid Acceptance criteria")
}

var pf6Re = regexp.MustCompile(`(?im)^##\s+Deviations\s*&\s*assumptions`)

// pf6Fix writes out the section PF6 accepts: a level-2 heading plus the
// four-column table from plan-format-reference.md's "Deviations & assumptions".
var pf6Fix = pfLines(
	`add this section after "## Context" and before the first task, as a level-2 heading outside any code fence:`,
	"  ## Deviations & assumptions",
	"  | Item | asked | does | why |",
	"  |---|---|---|---|",
	"  | what diverges or is assumed | yes or no | what the plan does | why |",
	`  (with nothing to record, keep the header row and add one row saying "none")`,
)

func checkPF6(content string) pfCheck {
	if !pf6Re.MatchString(stripFences(content)) {
		return pfFail("PF6", `Missing required "## Deviations & assumptions" section`, pf6Fix)
	}
	return pfPass("PF6", "Deviations & assumptions section present")
}

var (
	pf7BulletRe   = regexp.MustCompile(`(?m)^[-*]\s+(Create|Modify|Test):`)
	pf7ContractRe = regexp.MustCompile(`(?m)^\*\*Contract:\*\*`)
)

// contractBlockShape is the accepted **Contract:** block, shared by the PF7 and
// PF12 fixes. The five keys are the ones pf12RequiredKeyRes checks.
var contractBlockShape = pfLines(
	"  **Contract:**",
	"  - shape (<code|docs|openspec>): the decided shape of the deliverable",
	"  - names: exact symbols, IDs or headings this task introduces or touches",
	"  - mirror: the existing artifact this mirrors, with line anchors",
	"  - decisions: choices already made for this task",
	"  - sync: sibling artifacts that must stay consistent with it",
)

var pf7Fix = pfLines(
	"every task with a Create/Modify/Test bullet under **Files:** needs a **Contract:** block carrying all five keys:",
	contractBlockShape,
)

func checkPF7(tasks []planTask) pfCheck {
	var offenders []string
	for _, t := range tasks {
		if pf7BulletRe.MatchString(t.Body) && !pf7ContractRe.MatchString(t.Body) {
			offenders = append(offenders, fmt.Sprintf("Task %d", t.Number))
		}
	}
	if len(offenders) > 0 {
		return pfFail("PF7", fmt.Sprintf("Missing **Contract:** block: %s", strings.Join(offenders, ", ")), pf7Fix)
	}
	return pfPass("PF7", "All artifact-touching tasks have a Contract block")
}

// checkPF11 enforces the team-configured "plan.tasks.requiredFields" contract
// (loaded via loadPlanTasks in plan.go): every task must carry all
// configured custom fields, additive to the core 5 (Complexity, Risk, Files,
// Verify, Depends on) checked by PF3/PF4. loadPlanTasks already drops any
// entry duplicating a core field, so requiredFields here only ever contains
// genuinely custom fields. An empty requiredFields (absent config) degrades
// gracefully to a pass with no per-task looping.
func checkPF11(tasks []planTask, requiredFields []string) pfCheck {
	if len(requiredFields) == 0 {
		return pfPass("PF11", "No custom required fields configured")
	}
	var issues []string
	firstMissing := ""
	for _, t := range tasks {
		for _, field := range requiredFields {
			if v, ok := extractField(t.Body, field); !ok || v == "" {
				issues = append(issues, fmt.Sprintf("Task %d: missing required field '%s'", t.Number, field))
				if firstMissing == "" {
					firstMissing = field
				}
			}
		}
	}
	if len(issues) > 0 {
		// The headline names the config key: these fields are this project's
		// own requirement, not part of the built-in plan format.
		headline := "Missing custom task field(s) required by config key plan.tasks.requiredFields (project-specific, not built in):"
		fix := pfLines(
			fmt.Sprintf("add each missing field to its task as a bold label with the value on the same line, e.g. **%s:** <value>", firstMissing),
			"or remove the field from plan.tasks.requiredFields in the project SDLC config if it is no longer required",
		)
		return pfFail("PF11", pfIssueList(headline, issues), fix)
	}
	return pfPass("PF11", "All tasks have required custom fields")
}

// pf12ContractStartRe locates the content of a **Contract:** block (the
// indented "- key: value" list following the marker line), mirroring
// acStartRe/notesStartRe's extractDelimitedBlock usage in checkPF5.
var pf12ContractStartRe = regexp.MustCompile(`\*\*Contract:\*\*\s*\n`)

// pf12RequiredKeyRes are the five required Contract-block keys per
// plan-format-reference.md's "The `Contract:` block" section (shape, names,
// mirror, decisions, sync -- "example" is optional and not checked here).
var pf12RequiredKeyRes = []struct {
	name string
	re   *regexp.Regexp
}{
	{"shape", regexp.MustCompile(`(?m)^\s*-\s+shape\b`)},
	{"names", regexp.MustCompile(`(?m)^\s*-\s+names\b`)},
	{"mirror", regexp.MustCompile(`(?m)^\s*-\s+mirror\b`)},
	{"decisions", regexp.MustCompile(`(?m)^\s*-\s+decisions\b`)},
	{"sync", regexp.MustCompile(`(?m)^\s*-\s+sync\b`)},
}

// contractMissingKeys reports which of the five required Contract-block keys
// are absent from block (the block text following the **Contract:** marker).
func contractMissingKeys(block string) []string {
	var missing []string
	for _, k := range pf12RequiredKeyRes {
		if !k.re.MatchString(block) {
			missing = append(missing, k.name)
		}
	}
	return missing
}

// pf12Fix adds what the message does not say: how each key is written, and that
// the depth of this check is project-configured.
var pf12Fix = pfLines(
	`put all five keys under **Contract:**, one "- key: value" line each:`,
	contractBlockShape,
	`config key plan.tasks.contractShape sets how deep this is checked: "full" (default, all five keys), "minimal" (the block only), "none" (off)`,
)

// checkPF12 enforces the team-configured "plan.tasks.contractShape" contract
// (loaded via loadPlanTasks in plan.go) against artifact-touching tasks
// (same gating as PF7's pf7BulletRe):
//
//   - "none"    -- skip entirely (Contract block checks not enforced).
//   - "minimal" -- **Contract:** block must be present; depth not checked.
//   - "full"    -- (default) block must be present AND carry all five
//     required keys (shape, names, mirror, decisions, sync).
//
// An absent Contract block always fails for "full"/"minimal" (never for
// "none"). Core PF7 presence-only behavior is unaffected by this check.
func checkPF12(tasks []planTask, contractShape string) pfCheck {
	if contractShape == "" {
		contractShape = "full"
	}
	if contractShape == "none" {
		return pfPass("PF12", "Contract shape check skipped (contractShape: none)")
	}

	var issues []string
	for _, t := range tasks {
		if !pf7BulletRe.MatchString(t.Body) {
			continue
		}
		prefix := fmt.Sprintf("Task %d", t.Number)

		if !pf7ContractRe.MatchString(t.Body) {
			issues = append(issues, prefix+": missing **Contract:** block")
			continue
		}
		if contractShape == "minimal" {
			continue
		}

		block, _ := extractDelimitedBlock(t.Body, pf12ContractStartRe, []string{"\n**", "\n### ", "\n---", "\n## "})
		if missing := contractMissingKeys(block); len(missing) > 0 {
			issues = append(issues, fmt.Sprintf("%s: Contract block missing required key(s): %s", prefix, strings.Join(missing, ", ")))
		}
	}
	if len(issues) > 0 {
		return pfFail("PF12", pfIssueList(fmt.Sprintf("Contract block problems (contractShape: %s):", contractShape), issues), pf12Fix)
	}
	return pfPass("PF12", "All Contract blocks match required shape")
}

var pf9Re = regexp.MustCompile(`(?im)^##\s+Verification\s+Scorecard`)

// pf9Fix writes out the scorecard the plan skill assembles at Gate B (see the
// "Verification Scorecard" step in the plan SKILL.md). PF9 itself only checks
// that the heading exists; the three parts below are what the section carries.
var pf9Fix = pfLines(
	`add a level-2 heading "## Verification Scorecard" outside any code fence, with three parts:`,
	"  - a dimension table: one row each for Completeness, Correctness and Coherence, with counts of CRITICAL / WARNING / SUGGESTION / PASS findings",
	"  - a traceability matrix: one row per requirement, with the task(s) that cover it and a status of covered | partial | uncovered",
	`  - a verdict line: "All checks passed. Ready for archive." | "... Ready for archive (with noted improvements)." | "... Fix before archiving."`,
)

func checkPF9(content string) pfCheck {
	if pf9Re.MatchString(stripFences(content)) {
		return pfPass("PF9", "Verification Scorecard section present")
	}
	return pfFail("PF9", `Missing required "## Verification Scorecard" section`, pf9Fix)
}

var pf10WSRunRe = regexp.MustCompile(`\s+`)

func buildSectionHeadingRegex(name string) *regexp.Regexp {
	escaped := regexp.QuoteMeta(name)
	withWS := pf10WSRunRe.ReplaceAllString(escaped, `\s+`)
	// JS uses a trailing (?=\s|$) lookahead; Go's RE2 has no lookahead, but
	// since this is a presence test only (the match span is never used),
	// consuming the boundary char with (?:\s|$) instead of asserting it is
	// behaviorally identical here.
	return regexp.MustCompile(`(?im)^##\s+` + withWS + `(?:\s|$)`)
}

// checkPF10 checks the plan against the template's required sections.
// templatePath is the template those sections came from; it appears in the
// message so the author knows which template set the requirement.
func checkPF10(content string, sections []string, templatePath string) pfCheck {
	if len(sections) == 0 {
		return pfPass("PF10", "No template-required sections to check")
	}
	stripped := stripFences(content)
	var missing []string
	for _, name := range sections {
		if !buildSectionHeadingRegex(name).MatchString(stripped) {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		// The message names the sections. The fix adds the heading form that
		// counts: level 2, outside any code fence.
		fix := []string{"add each as a level-2 heading outside any code fence:"}
		for _, name := range missing {
			fix = append(fix, "  ## "+name)
		}
		return pfFail("PF10", fmt.Sprintf("Missing required section(s) from template %s: %s", templatePath, strings.Join(missing, ", ")), pfLines(fix...))
	}
	return pfPass("PF10", "All template-required sections present")
}

var (
	reqHeadingRe        = regexp.MustCompile(`(?m)^##\s+(.+)$`)
	reqListItemRe       = regexp.MustCompile(`(?m)^-\s+(.+)$`)
	narrativeTagRe      = regexp.MustCompile(`(?i)<!--\s*narrative:\s*true\s*-->`)
	conditionalTagRe    = regexp.MustCompile(`(?is)<!--\s*conditional:\s*(.+?)\s*-->`)
	conditionalStripRe2 = regexp.MustCompile(`(?is)<!--\s*conditional:.*?-->`)
)

// TemplateSection is a single "## Required Sections" bullet item, carrying
// its Narrative/Condition annotations alongside the section name.
type TemplateSection struct {
	Name      string  `json:"name"`
	Narrative bool    `json:"narrative"`
	Condition *string `json:"condition"`
}

// parseTemplateRequiredSectionsFull ports JS parseTemplate's scan of the
// "## Required Sections" heading block, returning each declared section's
// name plus its Narrative (<!-- narrative: true -->) and Condition
// (<!-- conditional: ... -->) annotations. This is the shared utility behind
// both template resolution and PF10 checking.
func parseTemplateRequiredSectionsFull(templatePath string) ([]TemplateSection, error) {
	content, err := os.ReadFile(templatePath)
	if err != nil {
		return nil, err
	}
	text := string(content)

	locs := reqHeadingRe.FindAllStringSubmatchIndex(text, -1)
	blockStart := -1
	blockEnd := len(text)
	for _, loc := range locs {
		headingText := strings.TrimSpace(text[loc[2]:loc[3]])
		if blockStart == -1 {
			if headingText == "Required Sections" {
				blockStart = loc[1]
			}
			continue
		}
		blockEnd = loc[0]
		break
	}
	if blockStart == -1 {
		return nil, nil
	}
	block := text[blockStart:blockEnd]

	var sections []TemplateSection
	for _, m := range reqListItemRe.FindAllStringSubmatch(block, -1) {
		line := strings.TrimSpace(m[1])

		narrative := narrativeTagRe.MatchString(line)
		line = strings.TrimSpace(narrativeTagRe.ReplaceAllString(line, ""))

		var condition *string
		if cm := conditionalTagRe.FindStringSubmatch(line); cm != nil {
			cond := strings.TrimSpace(cm[1])
			condition = &cond
			line = strings.TrimSpace(conditionalStripRe2.ReplaceAllString(line, ""))
		}

		if name := strings.TrimSpace(line); name != "" {
			sections = append(sections, TemplateSection{
				Name:      name,
				Narrative: narrative,
				Condition: condition,
			})
		}
	}
	return sections, nil
}

// parseTemplateRequiredSections returns just the section names from
// parseTemplateRequiredSectionsFull (Narrative/Condition annotations are
// stripped but not needed by PF10, which checks every declared section's
// heading unconditionally).
func parseTemplateRequiredSections(templatePath string) ([]string, error) {
	full, err := parseTemplateRequiredSectionsFull(templatePath)
	if err != nil {
		return nil, err
	}
	var sections []string
	for _, s := range full {
		sections = append(sections, s.Name)
	}
	return sections, nil
}

// ---------------------------------------------------------------------------
// pr_template (V1-V5) -- ports scripts/ci/validate-pr-template.js
//
// NOTE: this is unrelated to internal/prtemplate.ValidateBody (which checks
// a PR *body* against template headings for Task 25's pr_validate_body
// tool). This action validates the *template file itself*, reusing only
// prtemplate.Resolve for canonical/legacy path resolution.
// ---------------------------------------------------------------------------

type prSection struct {
	Name string
	Body string
}

func isPRHeadingLine(line string) bool {
	if !strings.HasPrefix(line, "## ") {
		return false
	}
	if len(line) > 3 && line[3] == '#' {
		return false
	}
	return true
}

func parsePRSections(content string) []prSection {
	lines := strings.Split(content, "\n")
	var sections []prSection
	haveHeading := false
	var currentHeading string
	var bodyLines []string

	flush := func() {
		if haveHeading {
			sections = append(sections, prSection{Name: currentHeading, Body: strings.TrimSpace(strings.Join(bodyLines, "\n"))})
		}
	}

	for _, line := range lines {
		if isPRHeadingLine(line) {
			flush()
			currentHeading = strings.TrimSpace(line[3:])
			bodyLines = nil
			haveHeading = true
		} else if haveHeading {
			bodyLines = append(bodyLines, line)
		}
	}
	flush()
	return sections
}

const prMinBodyLength = 20

func validatePRTemplate(root string) ([]discovery.Finding, error) {
	tmpl, err := prtemplate.Resolve(root)
	if err != nil {
		return nil, &mcpserver.InfraError{Msg: fmt.Sprintf("resolve pr template: %s", err.Error()), Suggestion: "Check read permission on " + paths.DataDir + "/pr-template.md and .claude/pr-template.md, then retry validate with action=\"pr_template\".", Cause: err}
	}

	var templatePath string
	if tmpl != nil {
		templatePath = tmpl.Path
	} else {
		templatePath = filepath.Join(root, paths.DataDir, paths.PRTemplateFile)
	}
	relPath, relErr := filepath.Rel(root, templatePath)
	if relErr != nil || relPath == "" {
		relPath = templatePath
	}

	var findings []discovery.Finding
	add := func(id, msg string) {
		findings = append(findings, discovery.Finding{ID: id, Severity: "error", Message: msg, Path: templatePath})
	}

	if tmpl == nil {
		add("V1", fmt.Sprintf("File not found: %s", relPath))
		return findings, nil
	}

	content := tmpl.Content
	if strings.TrimSpace(content) == "" {
		add("V2", "File is empty or contains only whitespace")
		return findings, nil
	}

	sections := parsePRSections(content)
	if len(sections) == 0 {
		add("V3", "No ## headings found in file")
		return findings, nil
	}

	// V4: duplicate heading names (case-insensitive), does not short-circuit V5.
	counts := map[string]int{}
	var order []string
	firstCasing := map[string]string{}
	for _, s := range sections {
		key := strings.ToLower(s.Name)
		if counts[key] == 0 {
			order = append(order, key)
			firstCasing[key] = s.Name
		}
		counts[key]++
	}
	var dupMsgs []string
	for _, key := range order {
		if counts[key] > 1 {
			dupMsgs = append(dupMsgs, fmt.Sprintf("'%s' appears %d times", firstCasing[key], counts[key]))
		}
	}
	if len(dupMsgs) > 0 {
		add("V4", fmt.Sprintf("Duplicate: %s", strings.Join(dupMsgs, "; ")))
	}

	// V5: every section body must be >= prMinBodyLength chars.
	var shortMsgs []string
	for _, s := range sections {
		if len(s.Body) < prMinBodyLength {
			shortMsgs = append(shortMsgs, fmt.Sprintf("Section '%s' has %d chars (min %d)", s.Name, len(s.Body), prMinBodyLength))
		}
	}
	if len(shortMsgs) > 0 {
		add("V5", strings.Join(shortMsgs, "; "))
	}

	return findings, nil
}

// ---------------------------------------------------------------------------
// pr_body -- delegates to pr.go's prValidateBodyCore, the former standalone
// pr_validate_body tool's logic (PR body vs. resolved template's section
// headings, per the pr SKILL.md's section-presence contract).
// ---------------------------------------------------------------------------

// validatePRBody adapts prValidateBodyCore's {OK bool, Errors []string}
// output into []discovery.Finding to match this dispatcher's uniform
// output shape. Each error string becomes one Finding; OK is not carried
// separately since it is redundant with len(Findings) == 0, matching this
// dispatcher's own "empty Findings means every check passed" convention
// (see package doc).
func validatePRBody(root string, in ValidateIn) ([]discovery.Finding, error) {
	out, err := prValidateBodyCore(root, PRValidateBodyIn{Body: in.Body})
	if err != nil {
		return nil, err
	}
	var findings []discovery.Finding
	for _, e := range out.Errors {
		findings = append(findings, discovery.Finding{ID: "PR_BODY", Severity: "error", Message: e})
	}
	return findings, nil
}

// ---------------------------------------------------------------------------
// cost_tiers -- ports scripts/ci/validate-cost-tiers.js
// ---------------------------------------------------------------------------

type costTierEntry struct {
	name  string
	model string // "" represents JS null (no model / inherited)
	file  string
}

type docRow struct {
	name  string
	model string
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

func resolveSkillsDir(root string) string {
	real := filepath.Join(root, "plugins", "sdlc", "skills")
	if isDir(real) {
		return real
	}
	flat := filepath.Join(root, "skills")
	if isDir(flat) {
		return flat
	}
	return ""
}

func resolveAgentsDir(root string) string {
	real := filepath.Join(root, "plugins", "sdlc", "agents")
	if isDir(real) {
		return real
	}
	flat := filepath.Join(root, "agents")
	if isDir(flat) {
		return flat
	}
	return ""
}

func frontmatterModel(content []byte) (name, model string, ok bool) {
	meta, _, err := frontmatter.Parse(content)
	if err != nil {
		return "", "", false
	}
	if n, isStr := meta["name"].(string); isStr && n != "" {
		name = n
	}
	if m, isStr := meta["model"].(string); isStr {
		model = m
	}
	return name, model, true
}

func scanSkills(root string) []costTierEntry {
	dir := resolveSkillsDir(root)
	if dir == "" {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []costTierEntry
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		skillFile := filepath.Join(dir, e.Name(), "SKILL.md")
		content, err := os.ReadFile(skillFile)
		if err != nil {
			continue
		}
		name, model, _ := frontmatterModel(content)
		if name == "" {
			name = e.Name()
		}
		out = append(out, costTierEntry{name: name, model: model, file: skillFile})
	}
	return out
}

func scanAgents(root string) []costTierEntry {
	dir := resolveAgentsDir(root)
	if dir == "" {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []costTierEntry
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		agentFile := filepath.Join(dir, e.Name())
		content, err := os.ReadFile(agentFile)
		if err != nil {
			continue
		}
		baseName := strings.TrimSuffix(e.Name(), ".md")
		name, model, _ := frontmatterModel(content)
		if name == "" {
			name = baseName
		}
		out = append(out, costTierEntry{name: name, model: model, file: agentFile})
	}
	return out
}

var (
	costTiersPipeLineRe = regexp.MustCompile(`^\s*\|`)
	costTiersSepLineRe  = regexp.MustCompile(`^\s*\|[\s|:-]+\|\s*$`)
	costTiersHashRe     = regexp.MustCompile(`^##\s`)
	skillTableHeadingRe = regexp.MustCompile(`^##\s+3\.\s+Skill Table`)
	agentTableHeadingRe = regexp.MustCompile(`^##\s+4\.\s+Agent Table`)
)

// Heading names the cost-tier parser looks for, shared by the parser's own
// error text and by validateCostTiers' recovery hint so the two cannot drift.
const (
	costTierSkillHeading = "## 3. Skill Table"
	costTierAgentHeading = "## 4. Agent Table"
)

// errCostTierDocRead marks a docs/cost-tiers.md read failure. It separates
// "the file is missing or unreadable" from "the file read fine but its
// tables are malformed", which need different recovery advice.
var errCostTierDocRead = errors.New("read cost-tier doc")

func splitTableRow(line string) []string {
	t := strings.TrimSpace(line)
	t = strings.TrimPrefix(t, "|")
	t = strings.TrimSuffix(t, "|")
	parts := strings.Split(t, "|")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

func readCostTierTableRows(lines []string, headerIdx int) ([]docRow, error) {
	expectedPipes := strings.Count(lines[headerIdx], "|")
	var rows []docRow
	i := headerIdx + 2
	for i < len(lines) {
		line := lines[i]
		if strings.TrimSpace(line) == "" || !strings.HasPrefix(strings.TrimSpace(line), "|") {
			break
		}
		pipeCount := strings.Count(line, "|")
		if pipeCount != expectedPipes {
			return nil, fmt.Errorf("docs/cost-tiers.md: row %d has %d pipes, expected %d: %s", i+1, pipeCount, expectedPipes, line)
		}
		cells := splitTableRow(line)
		r := docRow{}
		if len(cells) > 0 {
			r.name = cells[0]
		}
		if len(cells) > 1 {
			r.model = cells[1]
		}
		rows = append(rows, r)
		i++
	}
	return rows, nil
}

func findCostTierTableAfterHeading(lines []string, headingRe *regexp.Regexp, headingLabel string) ([]docRow, error) {
	headingIdx := -1
	for i, l := range lines {
		if headingRe.MatchString(l) {
			headingIdx = i
			break
		}
	}
	if headingIdx == -1 {
		return nil, fmt.Errorf("cost-tiers.md: table not found or empty (heading %q)", headingLabel)
	}
	for i := headingIdx + 1; i < len(lines); i++ {
		line := lines[i]
		if costTiersPipeLineRe.MatchString(line) && i+1 < len(lines) && costTiersSepLineRe.MatchString(lines[i+1]) {
			rows, err := readCostTierTableRows(lines, i)
			if err != nil {
				return nil, err
			}
			if len(rows) == 0 {
				return nil, fmt.Errorf("cost-tiers.md: table not found or empty (heading %q)", headingLabel)
			}
			return rows, nil
		}
		if costTiersHashRe.MatchString(line) {
			break
		}
	}
	return nil, fmt.Errorf("cost-tiers.md: table not found or empty (heading %q)", headingLabel)
}

func parseCostTierDocTables(root string) (skills, agents []docRow, err error) {
	docPath := filepath.Join(root, "docs", "cost-tiers.md")
	content, err := os.ReadFile(docPath)
	if err != nil {
		// os.ReadFile's error already names docPath; do not repeat it.
		return nil, nil, fmt.Errorf("%w: %w", errCostTierDocRead, err)
	}
	lines := strings.Split(string(content), "\n")

	skills, err = findCostTierTableAfterHeading(lines, skillTableHeadingRe, costTierSkillHeading)
	if err != nil {
		return nil, nil, err
	}
	agents, err = findCostTierTableAfterHeading(lines, agentTableHeadingRe, costTierAgentHeading)
	if err != nil {
		return nil, nil, err
	}
	return skills, agents, nil
}

// diffCostTier ports diffOne. kind embeds "skill"/"agent" into the message
// since discovery.Finding has no target field.
func diffCostTier(actuals []costTierEntry, docRows []docRow, kind string, strict bool) []discovery.Finding {
	docByName := map[string]string{}
	for _, r := range docRows {
		docByName[r.name] = r.model
	}
	actualByName := map[string]bool{}

	var findings []discovery.Finding
	for _, a := range actuals {
		actualByName[a.name] = true
		if a.model == "" {
			sev := "warning"
			if strict {
				sev = "error"
			}
			findings = append(findings, discovery.Finding{
				ID: "INHERITED", Severity: sev,
				Message: fmt.Sprintf("INHERITED (%s): %s", kind, a.name),
				Path:    a.file,
			})
			continue
		}
		docModel, inDoc := docByName[a.name]
		if !inDoc {
			findings = append(findings, discovery.Finding{
				ID: "MISSING_DOC", Severity: "error",
				Message: fmt.Sprintf("MISSING_DOC (%s): %s=%s", kind, a.name, a.model),
				Path:    a.file,
			})
			continue
		}
		if docModel != a.model {
			findings = append(findings, discovery.Finding{
				ID: "DRIFT", Severity: "error",
				Message: fmt.Sprintf("DRIFT (%s): %s frontmatter=%s doc=%s", kind, a.name, a.model, docModel),
				Path:    a.file,
			})
		}
	}
	for _, r := range docRows {
		if !actualByName[r.name] {
			findings = append(findings, discovery.Finding{
				ID: "STALE_DOC", Severity: "error",
				Message: fmt.Sprintf("STALE_DOC (%s): %s", kind, r.name),
				Path:    filepath.Join("docs", "cost-tiers.md"),
			})
		}
	}
	return findings
}

func validateCostTiers(root string, in ValidateIn) ([]discovery.Finding, error) {
	docSkills, docAgents, err := parseCostTierDocTables(root)
	if errors.Is(err, fs.ErrNotExist) {
		// A project without a cost-tier doc has nothing to compare against.
		// That is not a failure: report one warning so the caller sees the
		// check was skipped, rather than an empty (all-passed) list.
		return []discovery.Finding{{
			ID:       "NO_COST_DOC",
			Severity: "warning",
			Message:  "NO_COST_DOC: no cost-tier doc exists, so the cost_tiers check was skipped",
			Path:     filepath.Join("docs", "cost-tiers.md"),
		}}, nil
	}
	if err != nil {
		// The error text already carries the file path, so the Msg must not
		// repeat it, and an unreadable file needs different advice than a
		// malformed table.
		suggestion := fmt.Sprintf("Fix the %q and %q headings and the row format below them, then retry.", costTierSkillHeading, costTierAgentHeading)
		if errors.Is(err, errCostTierDocRead) {
			suggestion = "The cost-tier doc at the path named above exists but could not be read; check read permission on it (it must be a regular file), then retry."
		}
		return nil, &mcpserver.DataError{
			Msg:        fmt.Sprintf("cost-tier tables: %s", err.Error()),
			Suggestion: suggestion,
			Cause:      err,
		}
	}

	var findings []discovery.Finding
	findings = append(findings, diffCostTier(scanSkills(root), docSkills, "skill", in.Strict)...)
	findings = append(findings, diffCostTier(scanAgents(root), docAgents, "agent", in.Strict)...)
	return findings, nil
}

// ---------------------------------------------------------------------------
// guardrails -- ports scripts/ci/validate-guardrails.js
// ---------------------------------------------------------------------------

var guardrailKebabRe = regexp.MustCompile(`^[a-z][a-z0-9]*(-[a-z0-9]+)*$`)

// jsFalsyLocal mirrors JS truthiness for the small set of JSON-decoded
// types guardrail fields can take (nil, bool, string, float64).
func jsFalsyLocal(v any) bool {
	if v == nil {
		return true
	}
	switch x := v.(type) {
	case bool:
		return !x
	case string:
		return x == ""
	case float64:
		return x == 0
	}
	return false
}

func quotedList(items []string) string {
	q := make([]string, len(items))
	for i, s := range items {
		q[i] = fmt.Sprintf("%q", s)
	}
	return strings.Join(q, ", ")
}

// Guardrail finding fix hints. The id-format, duplicate-id, severity and
// length texts are pinned by the "guardrails finding fix hints" spec
// scenarios; the rest (missing/non-string id, description missing/wrong
// type/empty) are not pinned to exact wording but must be non-empty, per
// the "every finding has a fix" requirement.
const (
	guardrailIDMissingFix   = `Add a kebab-case id, e.g. no-ci-bypass, as the guardrail's key or "id" field.`
	guardrailIDFormatFix    = `Rename the id to lowercase words joined by single hyphens, e.g. no-ci-bypass.`
	guardrailIDDupFix       = `Use action consolidate on the current id, or pick a new unique id.`
	guardrailDescMissingFix = `Add a one-line description stating what the guardrail enforces.`
	guardrailDescTypeFix    = `Set description to a plain string.`
	guardrailDescEmptyFix   = `Add a non-empty description stating what the guardrail enforces.`
	guardrailSeverityFix    = `Set severity to error or warning.`
)

// guardrailDescLengthFix is the PF-style fix for an over-length description:
// it names the id-based split the author is expected to make, e.g. for id
// "x": "ids x-1, x-2".
func guardrailDescLengthFix(id string) string {
	return fmt.Sprintf("Shorten the description to 1024 bytes or less, or split it into independent guardrails with ids %s-1, %s-2, each a complete rule.", id, id)
}

// validateOneGuardrail ports validateGuardrail. All findings from this
// validator are errors -- the JS source's warnings array is never
// populated in practice (severity failures are pushed to errors too). Every
// finding carries a non-empty Fix (the repair step).
func validateOneGuardrail(g map[string]any, seenIDs map[string]bool) []discovery.Finding {
	var findings []discovery.Finding

	rawID := g["id"]
	idStr, idIsString := rawID.(string)
	displayID := "(missing)"
	if idIsString && idStr != "" {
		displayID = idStr
	}
	add := func(msg, fix string) {
		findings = append(findings, discovery.Finding{ID: displayID, Severity: "error", Message: fmt.Sprintf("%s: %s", displayID, msg), Path: "", Fix: fix})
	}

	if jsFalsyLocal(rawID) {
		add("id is missing", guardrailIDMissingFix)
	} else if !idIsString {
		add("id must be a string", guardrailIDMissingFix)
	} else {
		if !guardrailKebabRe.MatchString(idStr) {
			add("id must match kebab-case pattern: /^[a-z][a-z0-9]*(-[a-z0-9]+)*$/", guardrailIDFormatFix)
		}
		if seenIDs[idStr] {
			add("id is duplicated across guardrails", guardrailIDDupFix)
		} else {
			seenIDs[idStr] = true
		}
	}

	descVal := g["description"]
	descStr, descIsString := descVal.(string)
	if jsFalsyLocal(descVal) {
		add("description is missing", guardrailDescMissingFix)
	} else if !descIsString {
		add("description must be a string", guardrailDescTypeFix)
	} else if strings.TrimSpace(descStr) == "" {
		add("description cannot be empty", guardrailDescEmptyFix)
	}
	if !jsFalsyLocal(descVal) && descIsString && len(descStr) > 1024 {
		over := len(descStr) - 1024
		add(fmt.Sprintf("description exceeds 1024 bytes (%d bytes, %d over)", len(descStr), over), guardrailDescLengthFix(displayID))
	}

	if sevVal, present := g["severity"]; present && sevVal != nil {
		sevStr, isStr := sevVal.(string)
		if !isStr || !containsStr(dimensions.GuardrailSeverities, sevStr) {
			got := sevStr
			if !isStr {
				got = fmt.Sprint(sevVal)
			}
			add(fmt.Sprintf("severity must be %s, or undefined (got %q)", quotedList(dimensions.GuardrailSeverities), got), guardrailSeverityFix)
		}
	}

	return findings
}

// validateGuardrailsAction ports validateGuardrailsConfig. A missing or
// absent guardrails section is treated as zero guardrails configured (a
// pass), matching the JS "!sectionData || !Array.isArray(...)" branch. A
// missing section (config.ErrNotFound) still lets in.CandidatesJSON's
// entries be checked, against zero disk entries.
func validateGuardrailsAction(root string, in ValidateIn) ([]discovery.Finding, error) {
	section := in.Section
	if section == "" {
		section = "plan"
	}
	data, err := config.ReadSection(root, section)
	if err != nil && !errors.Is(err, config.ErrNotFound) {
		return nil, &mcpserver.InfraError{Msg: fmt.Sprintf("read %s guardrails section: %s", section, err.Error()), Suggestion: fmt.Sprintf("Check that config.toml's [%s] section is valid TOML and readable, then retry.", section), Cause: err}
	}

	// config.ReadSection always returns guardrails as []any: readProjectRaw
	// runs normalizeGuardrailTables on every project-section read, converting
	// the TOML named-table form ([plan.guardrails.<id>]) into this same
	// array-of-objects shape (with "id" injected from the table key) before
	// ReadSection ever extracts the section. There is no call path that
	// hands this function the raw map[string]any table shape. An array form
	// ([[plan.guardrails]] or an inline array) is not normalized and arrives
	// as written, so entries can lack an id or repeat one; a quoted empty
	// table key ([plan.guardrails.""]) injects id "". validateOneGuardrail's
	// missing-id and duplicate-id checks catch those cases.
	var raw []any
	if data != nil {
		raw, _ = data["guardrails"].([]any)
	}

	entries, reps, err := mergeGuardrailCandidates(raw, in.CandidatesJSON)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, nil
	}

	seen := map[string]bool{}
	var findings []discovery.Finding
	for _, g := range entries {
		findings = append(findings, validateOneGuardrail(g, seen)...)
	}
	findings = append(findings, guardrailSeverityDowngrades(reps)...)
	return findings, nil
}

// guardrailReplacement pairs a disk guardrail with the candidate that
// replaces it (same id) in mergeGuardrailCandidates.
type guardrailReplacement struct{ Disk, Candidate map[string]any }

// guardrailSeverityDowngradeFix is the repair step for a severity downgrade.
const guardrailSeverityDowngradeFix = `Keep severity error, or propose a new guardrail id for the weaker rule.`

// guardrailIsWarning reports whether a guardrail entry carries severity
// "warning". A missing or any other severity counts as error, the default.
func guardrailIsWarning(g map[string]any) bool {
	sev, _ := g["severity"].(string)
	return sev == "warning"
}

// guardrailSeverityDowngrades returns one finding per replacement that lowers
// severity. harden is strengthen-only, so a candidate may not weaken a disk
// entry that has the same id. A disk entry with no severity counts as error.
// Every finding is an error and carries a fix, mirroring validateOneGuardrail.
func guardrailSeverityDowngrades(reps []guardrailReplacement) []discovery.Finding {
	var findings []discovery.Finding
	for _, r := range reps {
		if guardrailIsWarning(r.Disk) || !guardrailIsWarning(r.Candidate) {
			continue
		}
		id, _ := r.Disk["id"].(string)
		findings = append(findings, discovery.Finding{
			ID:       id,
			Severity: "error",
			Message:  fmt.Sprintf("%s: severity lowered from error to warning (harden is strengthen-only)", id),
			Path:     "",
			Fix:      guardrailSeverityDowngradeFix,
		})
	}
	return findings
}

// mergeGuardrailCandidates merges candidatesJSON's proposed entries over the
// disk guardrails (raw, the section's "guardrails" array, possibly nil),
// entirely in memory -- nothing is written back. A candidate whose "id"
// equals a disk entry's "id" replaces that entry in place; a candidate with
// a new id (or no disk entries at all) is appended. Without candidatesJSON
// the disk entries are returned unchanged, with no replacement pairs.
// candidatesJSON that does not decode into a JSON array of objects is a
// DomainError; no entry is checked. The second return value lists each
// (disk, candidate) pair the merge replaced, in disk order, for
// guardrailSeverityDowngrades.
func mergeGuardrailCandidates(raw []any, candidatesJSON string) ([]map[string]any, []guardrailReplacement, error) {
	disk := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		if g, ok := item.(map[string]any); ok {
			disk = append(disk, g)
		}
	}
	if candidatesJSON == "" {
		return disk, nil, nil
	}

	var candidates []map[string]any
	if err := json.Unmarshal([]byte(candidatesJSON), &candidates); err != nil {
		return nil, nil, &mcpserver.DomainError{
			Msg:        fmt.Sprintf("guardrails: candidatesJson is not a JSON array of objects: %s", err.Error()),
			Suggestion: `Pass candidatesJson as a JSON array of guardrail objects, e.g. [{"id":"no-ci-bypass","description":"Plans must not skip CI.","severity":"error"}].`,
			Cause:      err,
		}
	}

	used := make([]bool, len(candidates))
	var reps []guardrailReplacement
	merged := make([]map[string]any, 0, len(disk)+len(candidates))
	for _, entry := range disk {
		id, _ := entry["id"].(string)
		replacement := -1
		if id != "" {
			for i, c := range candidates {
				if used[i] {
					continue
				}
				if cid, ok := c["id"].(string); ok && cid == id {
					replacement = i
					break
				}
			}
		}
		if replacement >= 0 {
			merged = append(merged, candidates[replacement])
			reps = append(reps, guardrailReplacement{Disk: entry, Candidate: candidates[replacement]})
			used[replacement] = true
		} else {
			merged = append(merged, entry)
		}
	}
	for i, c := range candidates {
		if !used[i] {
			merged = append(merged, c)
		}
	}
	return merged, reps, nil
}

// ---------------------------------------------------------------------------
// dimensions -- wraps internal/dimensions.{Load,Validate}, plus a net-new
// D10 cross-file duplicate-name check (dimensions.Validate only sees one
// Dimension at a time and cannot detect this).
// ---------------------------------------------------------------------------

func classifyDimensionMessage(msg string) (id, severity string) {
	switch {
	case strings.HasPrefix(msg, "Cannot read file:"):
		return "D0", "error"
	case msg == "Missing YAML frontmatter block (--- delimiters)":
		return "D1", "error"
	case msg == "Missing required field: name",
		msg == `Field "name" must be a string`,
		strings.HasPrefix(msg, `Field "name" must be lowercase letters, digits, and hyphens only`),
		strings.HasPrefix(msg, `Field "name" exceeds 64 characters`):
		return "D2", "error"
	case msg == "Missing required field: description",
		msg == `Field "description" must be a string`,
		msg == `Field "description" must not be empty`,
		strings.HasPrefix(msg, `Field "description" exceeds 256 characters`):
		return "D3", "error"
	case strings.HasPrefix(msg, "Missing required field: triggers"),
		msg == `Field "triggers" must be an array of strings`,
		msg == `Field "triggers" must contain at least one pattern`:
		return "D4", "error"
	case strings.HasPrefix(msg, "Invalid glob pattern in triggers:"):
		return "D5", "error"
	case strings.HasPrefix(msg, `Field "severity" must be one of:`):
		return "D6", "warning"
	case strings.HasPrefix(msg, `Field "max-files" must be a positive integer`):
		return "D7", "warning"
	case msg == `Field "skip-when" must be an array of strings`,
		strings.HasPrefix(msg, "Invalid glob pattern in skip-when:"):
		return "D8", "warning"
	case strings.HasPrefix(msg, "Body must contain at least 10 characters"):
		return "D9", "error"
	case strings.HasPrefix(msg, "Unknown frontmatter field:"):
		return "D11", "warning"
	case strings.HasPrefix(msg, `Field "requires-full-diff" must be a boolean`):
		return "D12", "warning"
	case strings.HasPrefix(msg, `Field "model" must be a non-empty string`):
		return "D13", "warning"
	default:
		return "UNKNOWN", "error"
	}
}

// ---------------------------------------------------------------------------
// Exported wrappers (Task 38) -- internal/hooks needs to call these
// validators directly (post-tool-validate.js's in-process port), but Go
// visibility makes the unexported functions above uncallable cross-package.
// Each wrapper is a pure one-line delegate; the unexported functions and the
// validate dispatcher above are unchanged.
//
// Two wrappers live here, both called from internal/hooks/post_tool_validate.go.
// The hook's plan_format entry point is NOT one of them: it is
// ValidatePlanFormatForHook above, which splits blocking from final-only
// findings and reuses one read of the plan. A plain ValidatePlanFormat
// delegate existed here and was removed once nothing called it.
// ---------------------------------------------------------------------------

// ValidatePRTemplate delegates to validatePRTemplate for internal/hooks.
func ValidatePRTemplate(root string) ([]discovery.Finding, error) {
	return validatePRTemplate(root)
}

// ValidateDimensionsAction delegates to validateDimensionsAction for internal/hooks.
func ValidateDimensionsAction(root string) ([]discovery.Finding, error) {
	return validateDimensionsAction(root)
}

func validateDimensionsAction(root string) ([]discovery.Finding, error) {
	dir := filepath.Join(root, paths.DataDir, paths.ReviewDimensionsSubdir)
	dims, err := dimensions.Load(dir)
	if err != nil {
		return nil, &mcpserver.InfraError{Msg: fmt.Sprintf("load review dimensions: %s", err.Error()), Suggestion: "Check filesystem permissions on " + paths.DataDir + "/review-dimensions/, then retry validate with action=\"dimensions\".", Cause: err}
	}

	var findings []discovery.Finding
	seenNames := map[string]string{}

	for _, d := range dims {
		for _, msg := range dimensions.Validate(d) {
			id, sev := classifyDimensionMessage(msg)
			findings = append(findings, discovery.Finding{ID: id, Severity: sev, Message: msg, Path: d.File})
		}

		if name, ok := d.Meta["name"].(string); ok && name != "" {
			if firstFile, dup := seenNames[name]; dup {
				findings = append(findings, discovery.Finding{
					ID:       "D10",
					Severity: "error",
					Message:  fmt.Sprintf("Duplicate dimension name %q — also used in %s", name, filepath.Base(firstFile)),
					Path:     d.File,
				})
			} else {
				seenNames[name] = d.File
			}
		}
	}

	return findings, nil
}

// ---------------------------------------------------------------------------
// ci_script_drift (Task 3, R2) -- flags CI scaffold scripts/workflows that
// are outdated or not yet installed, reusing scaffold.go's ciScriptDrift
// (also surfaced directly via setup_prepare's CIScriptDrift field). Only
// non-"current" entries produce a finding -- an up-to-date script is not
// drift, matching this dispatcher's "empty Findings means every check
// passed" convention (see package doc).
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// worktree_anchoring (Task 4, R1) -- reports which git worktree (main or
// active) the .sdlc-v2/ state directory is currently anchored to, and flags
// two failure modes in that anchoring:
//
//   - the resolved main worktree is itself a bare repository (should never
//     happen after mainRootIn's bare-entry skip in internal/worktree, but
//     checked here as a regression guard rather than trusted silently);
//   - .sdlc-v2/ exists under the ACTIVE worktree but not the main one, which
//     means state/config reads and writes are split across worktrees (the
//     visible symptom of the bare-anchoring bug this task fixes, and of any
//     other bug in the same family).
//
// Known adjacent issue, documented here but deliberately NOT changed by this
// task: internal/hooks/post_tool_validate.go:45 resolves its project root
// via os.Getwd() rather than worktree.MainRoot() (a deliberate JS-source
// ruling — see that file's own comment). In a linked worktree, .sdlc-v2/ may
// not exist at cwd, so that hook silently no-ops instead of validating
// anything. Same bug family as this task's fix, but a different code path
// and out of scope here.
// ---------------------------------------------------------------------------

// WorktreeAnchoringCheck reports which git worktree (main or active) the
// .sdlc-v2/ state directory is anchored to, so operators can diagnose the
// "state written to the wrong worktree" bug family (Task 4/R1) instead of
// hitting a silent no-op or a mysteriously empty state/config read.
type WorktreeAnchoringCheck struct {
	MainRoot      string `json:"mainRoot"`
	ActiveRoot    string `json:"activeRoot"`
	IsLinked      bool   `json:"isLinked"`
	IsBare        bool   `json:"isBare"`
	StateDir      string `json:"stateDir"`
	StateDirOwner string `json:"stateDirOwner"` // main|active
}

// sameWorktreePath reports whether a and b resolve to the same real path,
// falling back to a plain string comparison if either fails to resolve
// (e.g. a path that no longer exists) rather than erroring out.
func sameWorktreePath(a, b string) bool {
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	if errA != nil || errB != nil {
		return a == b
	}
	return ra == rb
}

// isCorrectStateLink reports whether name is one of paths.LinkedStateEntries
// and the corresponding entry under <activeRoot>/.sdlc-v2/ is a symlink whose
// target is <mainRoot>/.sdlc-v2/<name>. Entries that are never linked
// (paths.UnlinkedStateEntries) always return false here, so they keep
// today's stray-detection behavior unchanged (F-worktree-state-links-8).
//
// The comparison below must accept a dangling link: the worktree-state-links
// spec's "Link creation trigger" requirement has SessionStart create a
// symlink to a main-worktree entry that may not exist yet (nothing has
// written run state there), so filepath.EvalSymlinks — which requires its
// argument to resolve through to an existing file — cannot be used on either
// side here, unlike sameWorktreePath above. Instead, resolveBestEffort
// resolves symlinks in whatever prefix of each path actually exists (at
// least mainRoot/activeRoot themselves, which are real worktree roots) and
// leaves the rest of the path unresolved but Clean-normalized, then the two
// results are compared as plain strings.
func isCorrectStateLink(mainRoot, activeRoot, name string) bool {
	linked := false
	for _, n := range paths.LinkedStateEntries {
		if n == name {
			linked = true
			break
		}
	}
	if !linked {
		return false
	}

	linkPath := filepath.Join(activeRoot, paths.DataDir, name)
	info, err := os.Lstat(linkPath)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		return false
	}

	target, err := os.Readlink(linkPath)
	if err != nil {
		return false
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(linkPath), target)
	}

	wantTarget := filepath.Join(mainRoot, paths.DataDir, name)
	return resolveBestEffort(target) == resolveBestEffort(wantTarget)
}

// resolveBestEffort resolves symlinks in whatever leading prefix of path
// actually exists on disk, then rejoins the remaining (possibly
// nonexistent) suffix unresolved. filepath.EvalSymlinks fails outright when
// any part of path does not exist, which is too strict for comparing a
// dangling state-link target against its expected location: the ancestor
// worktree root exists, but the .sdlc-v2/<entry> leaf may not, yet.
func resolveBestEffort(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	parent := filepath.Dir(path)
	if parent == path {
		return filepath.Clean(path)
	}
	return filepath.Join(resolveBestEffort(parent), filepath.Base(path))
}

// findStrayStateEntries reports every top-level entry inside
// <activeRoot>/.sdlc-v2/ that is not part of the committable set (see
// CommittableStateDirEntries in setup.go). Every linked worktree
// legitimately has .gitignore, config.toml, and review-dimensions/ because
// they're tracked in git -- anything else there is worktree-local state
// that leaked into the linked worktree instead of landing in the main
// worktree's .sdlc-v2/ (the bug family this check exists to catch). A
// missing directory is not an error: a linked worktree with no .sdlc-v2/ at
// all has nothing stray to report. A correctly-linked entry (see
// isCorrectStateLink) is also not stray -- F-worktree-state-links-7 links
// run-generated entries into the active worktree on purpose.
func findStrayStateEntries(mainRoot, activeRoot string) ([]discovery.Finding, error) {
	activeDir := filepath.Join(activeRoot, paths.DataDir)
	entries, err := os.ReadDir(activeDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", activeDir, err)
	}

	allowed := make(map[string]bool, len(CommittableStateDirEntries))
	for _, e := range CommittableStateDirEntries {
		allowed[e.Name] = true
	}

	var findings []discovery.Finding
	for _, entry := range entries {
		if allowed[entry.Name()] {
			continue
		}
		if isCorrectStateLink(mainRoot, activeRoot, entry.Name()) {
			continue
		}
		findings = append(findings, discovery.Finding{
			ID:       "WORKTREE_ANCHOR_STRAY_STATE",
			Severity: "error",
			Message: fmt.Sprintf(
				"%s exists in the linked worktree; state belongs in %s/",
				entry.Name(), filepath.Join(mainRoot, paths.DataDir)),
			Path: filepath.Join(activeDir, entry.Name()),
		})
	}
	return findings, nil
}

// resolveStateDirOwner reports which worktree currently owns the .sdlc-v2/
// directory on disk: "main" when it exists under mainRoot (the canonical
// anchor per internal/worktree's package doc), "active" when it exists only
// under activeRoot (the misanchoring symptom this check exists to catch), or
// "main" with the canonical (not-yet-created) path when neither exists yet.
func resolveStateDirOwner(mainRoot, activeRoot string) (dir, owner string, statErr error) {
	mainDir := filepath.Join(mainRoot, paths.DataDir)
	if _, err := os.Stat(mainDir); err == nil {
		return mainDir, "main", nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return mainDir, "main", fmt.Errorf("stat %s: %w", mainDir, err)
	}
	activeDir := filepath.Join(activeRoot, paths.DataDir)
	if _, err := os.Stat(activeDir); err == nil {
		return activeDir, "active", nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return activeDir, "active", fmt.Errorf("stat %s: %w", activeDir, err)
	}
	return mainDir, "main", nil
}

// validateWorktreeAnchoring builds the WorktreeAnchoringCheck and raises
// findings for its two failure modes (see package doc above). root is
// already resolved via worktree.MainRoot() by RegisterValidateTools, so it
// is used directly as the check's MainRoot rather than re-resolving it.
func validateWorktreeAnchoring(root string) (*WorktreeAnchoringCheck, []discovery.Finding, error) {
	activeRoot, err := worktree.ActiveRoot()
	if err != nil {
		return nil, nil, &mcpserver.InfraError{Msg: fmt.Sprintf("resolve active worktree: %s", err.Error()), Suggestion: "Run validate with action=\"worktree_anchoring\" from inside a git working tree; no active worktree could be resolved.", Cause: err}
	}

	isBare, err := worktree.IsBare(root)
	if err != nil {
		return nil, nil, &mcpserver.InfraError{Msg: fmt.Sprintf("determine bare status: %s", err.Error()), Suggestion: fmt.Sprintf("Check that %s is a valid, readable git repository (its .git directory or file), then retry.", root), Cause: err}
	}

	stateDir, owner, statErr := resolveStateDirOwner(root, activeRoot)
	if statErr != nil {
		return nil, nil, &mcpserver.InfraError{Msg: fmt.Sprintf("resolve state dir owner: %s", statErr.Error()), Suggestion: "Check filesystem permissions on " + paths.DataDir + " under both the main root and the active worktree root, then retry.", Cause: statErr}
	}

	check := &WorktreeAnchoringCheck{
		MainRoot:      root,
		ActiveRoot:    activeRoot,
		IsLinked:      !sameWorktreePath(root, activeRoot),
		IsBare:        isBare,
		StateDir:      stateDir,
		StateDirOwner: owner,
	}

	var findings []discovery.Finding
	if isBare {
		findings = append(findings, discovery.Finding{
			ID:       "WORKTREE_ANCHOR_BARE",
			Severity: "error",
			Message:  fmt.Sprintf("resolved main worktree %q is a bare repository -- %s would anchor to a root with no working tree", root, paths.DataDir),
			Path:     root,
		})
	}
	if owner == "active" {
		findings = append(findings, discovery.Finding{
			ID:       "WORKTREE_ANCHOR_MISMATCH",
			Severity: "warning",
			Message: fmt.Sprintf(
				"%s exists under the active worktree (%s) but not the main worktree (%s) -- state/config may be split across worktrees",
				paths.DataDir, activeRoot, root),
			Path: stateDir,
		})
	}
	if check.IsLinked {
		strayFindings, err := findStrayStateEntries(root, activeRoot)
		if err != nil {
			return nil, nil, &mcpserver.InfraError{Msg: fmt.Sprintf("scan linked worktree state: %s", err.Error()), Cause: err, Suggestion: "Check that both the main and active worktree state directories are readable, then retry."}
		}
		findings = append(findings, strayFindings...)
	}

	return check, findings, nil
}

func validateCIScriptDrift(root string) ([]discovery.Finding, error) {
	entries, err := ciScriptDrift(root)
	if err != nil {
		return nil, err
	}

	var findings []discovery.Finding
	for _, e := range entries {
		switch e.Action {
		case "outdated":
			findings = append(findings, discovery.Finding{
				ID:       "CI_SCRIPT_OUTDATED",
				Severity: "warning",
				Message: fmt.Sprintf(
					"%s is outdated (installed v%d, current v%d) — run scaffold_ci({force:true}) to update.",
					e.Script, e.InstalledVersion, e.CurrentVersion),
				Path: e.Script,
			})
		case "missing":
			findings = append(findings, discovery.Finding{
				ID:       "CI_SCRIPT_MISSING",
				Severity: "warning",
				Message: fmt.Sprintf(
					"%s is not installed (current v%d) — run scaffold_ci({force:true}) to install.",
					e.Script, e.CurrentVersion),
				Path: e.Script,
			})
		}
	}
	return findings, nil
}
