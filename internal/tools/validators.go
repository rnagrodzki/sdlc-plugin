// Package tools: validate action-enum tool (Task 36).
//
// Ports six source validators behind one MCP tool ("validate"), following
// the KD16 action-enum precedent established by Task 35's ship_state:
//
//   - plan_format  -- scripts/ci/validate-plan-format.js  (PF1-PF7, PF9, PF10)
//   - discovery    -- internal/discovery.ValidateAll       (PD1-PD16, reused as-is)
//   - pr_template  -- scripts/ci/validate-pr-template.js   (V1-V5)
//   - cost_tiers   -- scripts/ci/validate-cost-tiers.js    (DRIFT/MISSING_DOC/STALE_DOC/INHERITED)
//   - guardrails    -- scripts/ci/validate-guardrails.js    (per-guardrail id/description/severity)
//   - dimensions   -- internal/dimensions.Validate, plus a net-new D10
//     cross-file duplicate-name check (dimensions.Validate only checks one
//     Dimension at a time and cannot see other files).
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
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/rnagrodzki/sdlc-plugin/internal/config"
	"github.com/rnagrodzki/sdlc-plugin/internal/dimensions"
	"github.com/rnagrodzki/sdlc-plugin/internal/discovery"
	"github.com/rnagrodzki/sdlc-plugin/internal/frontmatter"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/prtemplate"
	"github.com/rnagrodzki/sdlc-plugin/internal/worktree"
)

// ---------------------------------------------------------------------------
// Tool contract
// ---------------------------------------------------------------------------

// ValidateIn is the input for the "validate" tool.
type ValidateIn struct {
	// Action selects the validator: plan_format | discovery | pr_template |
	// cost_tiers | guardrails | dimensions.
	Action string `json:"action"`
	// File is the target file for plan_format and links... (plan_format only
	// here; links_validate lives in links.go).
	File string `json:"file,omitempty"`
	// Final requests the stricter plan_format checks (PF9/PF10), matching
	// the JS --final flag.
	Final bool `json:"final,omitempty"`
	// Template is the plan template path for plan_format's PF10 check.
	Template string `json:"template,omitempty"`
	// Strict controls whether cost_tiers' INHERITED kind is severity
	// "error" (true) or "warning" (false), matching the JS --strict flag.
	Strict bool `json:"strict,omitempty"`
	// Section is the config section guardrails reads its guardrails list
	// from. Defaults to "plan" when empty, matching the JS default.
	Section string `json:"section,omitempty"`
}

// ValidateOut is the output for the "validate" tool.
type ValidateOut struct {
	Findings []discovery.Finding `json:"findings"`
}

// RegisterValidateTools registers the "validate" MCP tool.
func RegisterValidateTools(s *mcpserver.Server) {
	mcpserver.Register(s, "validate",
		"Run a deterministic validator against the project: plan_format, discovery, pr_template, cost_tiers, guardrails, or dimensions. Returns structured findings (id, severity, message, path) for failed checks only.",
		func(ctx mcpserver.Ctx, in ValidateIn) (any, error) {
			root, err := worktree.MainRoot()
			if err != nil {
				return nil, &mcpserver.InfraError{Msg: fmt.Sprintf("resolve project root: %s", err.Error()), Cause: err}
			}
			return validate(root, in)
		},
	)
}

func validate(root string, in ValidateIn) (ValidateOut, error) {
	var (
		findings []discovery.Finding
		err      error
	)
	switch in.Action {
	case "plan_format":
		findings, err = validatePlanFormat(root, in)
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
	default:
		return ValidateOut{}, &mcpserver.DomainError{Msg: fmt.Sprintf("unknown validate action %q", in.Action)}
	}
	if err != nil {
		return ValidateOut{}, err
	}
	return ValidateOut{Findings: findings}, nil
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

type pfCheck struct {
	id      string
	status  string // "pass" | "fail"
	message string
}

type planTask struct {
	Number int
	Title  string
	Body   string
}

func validatePlanFormat(root string, in ValidateIn) ([]discovery.Finding, error) {
	if in.File == "" {
		return nil, &mcpserver.DomainError{Msg: "plan_format: file is required"}
	}
	filePath := resolvePath(root, in.File)
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, &mcpserver.DomainError{Msg: fmt.Sprintf("plan_format: file not found: %s", filePath), Cause: err}
	}
	content := string(data)
	tasks := extractTasks(content)

	checks := []pfCheck{
		checkPF1(content),
		checkPF2(tasks),
		checkPF3(tasks),
		checkPF4(tasks),
		checkPF5(tasks),
		checkPF6(content),
		checkPF7(tasks),
	}

	if in.Final {
		checks = append(checks, checkPF9(content))
		if in.Template != "" {
			templatePath := resolvePath(root, in.Template)
			sections, terr := parseTemplateRequiredSections(templatePath)
			if terr != nil {
				return nil, &mcpserver.DomainError{Msg: fmt.Sprintf("plan_format: template not found: %s", templatePath), Cause: terr}
			}
			checks = append(checks, checkPF10(content, sections))
		}
	}

	var findings []discovery.Finding
	for _, c := range checks {
		if c.status == "fail" {
			findings = append(findings, discovery.Finding{ID: c.id, Severity: "error", Message: c.message, Path: filePath})
		}
	}
	return findings, nil
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
	return strings.TrimSpace(m[1]), true
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

func checkPF1(content string) pfCheck {
	fields := []string{"Goal", "Architecture", "Source", "Verification"}
	var missing []string
	for _, f := range fields {
		v, ok := extractField(content, f)
		if !ok || v == "" {
			missing = append(missing, f)
		}
	}
	if len(missing) > 0 {
		return pfCheck{"PF1", "fail", fmt.Sprintf("Missing or empty header field(s): %s", strings.Join(missing, ", "))}
	}
	return pfCheck{"PF1", "pass", "All header fields present"}
}

func checkPF2(tasks []planTask) pfCheck {
	if len(tasks) == 0 {
		return pfCheck{"PF2", "fail", "No tasks found (expected ### Task N: format)"}
	}
	numbers := make([]int, len(tasks))
	for i, t := range tasks {
		numbers[i] = t.Number
	}
	sort.Ints(numbers)
	start := numbers[0]
	if start != 0 && start != 1 {
		return pfCheck{"PF2", "fail", fmt.Sprintf("Task numbering must start at 0 or 1, found: %d", start)}
	}
	var gaps []string
	var dupes []string
	for i := 1; i < len(numbers); i++ {
		if numbers[i] == numbers[i-1] {
			dupes = append(dupes, strconv.Itoa(numbers[i]))
		} else if numbers[i] != numbers[i-1]+1 {
			gaps = append(gaps, fmt.Sprintf("gap between Task %d and Task %d", numbers[i-1], numbers[i]))
		}
	}
	var issues []string
	if len(gaps) > 0 {
		issues = append(issues, fmt.Sprintf("non-contiguous numbering: %s", strings.Join(gaps, ", ")))
	}
	if len(dupes) > 0 {
		issues = append(issues, fmt.Sprintf("duplicate task number(s): %s", strings.Join(dupes, ", ")))
	}
	if len(issues) > 0 {
		return pfCheck{"PF2", "fail", fmt.Sprintf("Task numbering issues: %s", strings.Join(issues, "; "))}
	}
	return pfCheck{"PF2", "pass", fmt.Sprintf("%d task(s) numbered contiguously from %d", len(tasks), start)}
}

var (
	validComplexity = []string{"Trivial", "Standard", "Complex"}
	validRisk       = []string{"Low", "Medium", "High"}
	validVerify     = []string{"tests", "build", "lint", "manual"}
	verifySplitRe   = regexp.MustCompile(`,\s*`)
)

func checkPF3(tasks []planTask) pfCheck {
	var issues []string
	for _, t := range tasks {
		prefix := fmt.Sprintf("Task %d", t.Number)

		if complexity, ok := extractField(t.Body, "Complexity"); !ok || complexity == "" {
			issues = append(issues, prefix+": missing **Complexity:**")
		} else if !containsStr(validComplexity, complexity) {
			issues = append(issues, fmt.Sprintf("%s: invalid Complexity %q (expected: %s)", prefix, complexity, strings.Join(validComplexity, "|")))
		}

		if risk, ok := extractField(t.Body, "Risk"); !ok || risk == "" {
			issues = append(issues, prefix+": missing **Risk:**")
		} else if !containsStr(validRisk, risk) {
			issues = append(issues, fmt.Sprintf("%s: invalid Risk %q (expected: %s)", prefix, risk, strings.Join(validRisk, "|")))
		}

		if dependsOn, ok := extractField(t.Body, "Depends on"); !ok || dependsOn == "" {
			issues = append(issues, prefix+": missing **Depends on:**")
		}

		verify, ok := extractField(t.Body, "Verify")
		if !ok || verify == "" {
			issues = append(issues, prefix+": missing **Verify:**")
		} else {
			var invalid []string
			for _, v := range verifySplitRe.Split(verify, -1) {
				v = strings.TrimSpace(v)
				if !containsStr(validVerify, v) {
					invalid = append(invalid, v)
				}
			}
			if len(invalid) > 0 {
				issues = append(issues, fmt.Sprintf("%s: invalid Verify value(s): %s (expected: %s)", prefix, strings.Join(invalid, ", "), strings.Join(validVerify, "|")))
			}
		}
	}
	if len(issues) > 0 {
		return pfCheck{"PF3", "fail", strings.Join(issues, "; ")}
	}
	return pfCheck{"PF3", "pass", "All tasks have valid metadata"}
}

var pf4RefRe = regexp.MustCompile(`(?i)Task\s+(\d+)`)

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
		var refs []int
		for _, m := range pf4RefRe.FindAllStringSubmatch(dependsOn, -1) {
			refNum, _ := strconv.Atoi(m[1])
			refs = append(refs, refNum)
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
		return pfCheck{"PF4", "fail", strings.Join(issues, "; ")}
	}
	return pfCheck{"PF4", "pass", "All dependencies valid, no cycles"}
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
		return pfCheck{"PF5", "fail", strings.Join(issues, "; ")}
	}
	return pfCheck{"PF5", "pass", "All tasks have valid Acceptance criteria"}
}

var pf6Re = regexp.MustCompile(`(?im)^##\s+Deviations\s*&\s*assumptions`)

func checkPF6(content string) pfCheck {
	if !pf6Re.MatchString(stripFences(content)) {
		return pfCheck{"PF6", "fail", `Missing required "## Deviations & assumptions" section`}
	}
	return pfCheck{"PF6", "pass", "Deviations & assumptions section present"}
}

var (
	pf7BulletRe   = regexp.MustCompile(`(?m)^[-*]\s+(Create|Modify|Test):`)
	pf7ContractRe = regexp.MustCompile(`(?m)^\*\*Contract:\*\*`)
)

func checkPF7(tasks []planTask) pfCheck {
	var offenders []string
	for _, t := range tasks {
		if pf7BulletRe.MatchString(t.Body) && !pf7ContractRe.MatchString(t.Body) {
			offenders = append(offenders, fmt.Sprintf("Task %d", t.Number))
		}
	}
	if len(offenders) > 0 {
		return pfCheck{"PF7", "fail", fmt.Sprintf("Missing **Contract:** block: %s", strings.Join(offenders, ", "))}
	}
	return pfCheck{"PF7", "pass", "All artifact-touching tasks have a Contract block"}
}

var pf9Re = regexp.MustCompile(`(?im)^##\s+Verification\s+Scorecard`)

func checkPF9(content string) pfCheck {
	if pf9Re.MatchString(stripFences(content)) {
		return pfCheck{"PF9", "pass", "Verification Scorecard section present"}
	}
	return pfCheck{"PF9", "fail", `Missing required "## Verification Scorecard" section`}
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

func checkPF10(content string, sections []string) pfCheck {
	if len(sections) == 0 {
		return pfCheck{"PF10", "pass", "No template-required sections to check"}
	}
	stripped := stripFences(content)
	var missing []string
	for _, name := range sections {
		if !buildSectionHeadingRegex(name).MatchString(stripped) {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return pfCheck{"PF10", "fail", fmt.Sprintf("Missing required section(s): %s", strings.Join(missing, ", "))}
	}
	return pfCheck{"PF10", "pass", "All template-required sections present"}
}

var (
	reqHeadingRe        = regexp.MustCompile(`(?m)^##\s+(.+)$`)
	reqListItemRe       = regexp.MustCompile(`(?m)^-\s+(.+)$`)
	narrativeTagRe      = regexp.MustCompile(`(?i)<!--\s*narrative:\s*true\s*-->`)
	conditionalTagRe    = regexp.MustCompile(`(?is)<!--\s*conditional:\s*(.+?)\s*-->`)
	conditionalStripRe2 = regexp.MustCompile(`(?is)<!--\s*conditional:.*?-->`)
)

// parseTemplateRequiredSections ports JS parseTemplate's scan of the
// "## Required Sections" heading block, returning just the section names
// (Narrative/Condition annotations are stripped but not needed by PF10,
// which checks every declared section's heading unconditionally).
func parseTemplateRequiredSections(templatePath string) ([]string, error) {
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

	var sections []string
	for _, m := range reqListItemRe.FindAllStringSubmatch(block, -1) {
		line := strings.TrimSpace(m[1])
		line = strings.TrimSpace(narrativeTagRe.ReplaceAllString(line, ""))
		if conditionalTagRe.MatchString(line) {
			line = strings.TrimSpace(conditionalStripRe2.ReplaceAllString(line, ""))
		}
		if name := strings.TrimSpace(line); name != "" {
			sections = append(sections, name)
		}
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
		return nil, &mcpserver.InfraError{Msg: fmt.Sprintf("resolve pr template: %s", err.Error()), Cause: err}
	}

	var templatePath string
	if tmpl != nil {
		templatePath = tmpl.Path
	} else {
		templatePath = filepath.Join(root, ".sdlc", "pr-template.md")
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
	real := filepath.Join(root, "plugins", "sdlc-utilities", "skills")
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
	real := filepath.Join(root, "plugins", "sdlc-utilities", "agents")
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
		return nil, nil, fmt.Errorf("docs/cost-tiers.md: not found: %w", err)
	}
	lines := strings.Split(string(content), "\n")

	skills, err = findCostTierTableAfterHeading(lines, skillTableHeadingRe, "## 3. Skill Table")
	if err != nil {
		return nil, nil, err
	}
	agents, err = findCostTierTableAfterHeading(lines, agentTableHeadingRe, "## 4. Agent Table")
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
	skills := scanSkills(root)
	agents := scanAgents(root)

	docSkills, docAgents, err := parseCostTierDocTables(root)
	if err != nil {
		return nil, &mcpserver.DataError{Msg: err.Error(), Cause: err}
	}

	var findings []discovery.Finding
	findings = append(findings, diffCostTier(skills, docSkills, "skill", in.Strict)...)
	findings = append(findings, diffCostTier(agents, docAgents, "agent", in.Strict)...)
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

// validateOneGuardrail ports validateGuardrail. All findings from this
// validator are errors -- the JS source's warnings array is never
// populated in practice (severity failures are pushed to errors too).
func validateOneGuardrail(g map[string]any, seenIDs map[string]bool) []discovery.Finding {
	var findings []discovery.Finding

	rawID := g["id"]
	idStr, idIsString := rawID.(string)
	displayID := "(missing)"
	if idIsString && idStr != "" {
		displayID = idStr
	}
	add := func(msg string) {
		findings = append(findings, discovery.Finding{ID: displayID, Severity: "error", Message: fmt.Sprintf("%s: %s", displayID, msg), Path: ""})
	}

	if jsFalsyLocal(rawID) {
		add("id is missing")
	} else if !idIsString {
		add("id must be a string")
	} else {
		if !guardrailKebabRe.MatchString(idStr) {
			add("id must match kebab-case pattern: /^[a-z][a-z0-9]*(-[a-z0-9]+)*$/")
		}
		if seenIDs[idStr] {
			add("id is duplicated across guardrails")
		} else {
			seenIDs[idStr] = true
		}
	}

	descVal := g["description"]
	descStr, descIsString := descVal.(string)
	if jsFalsyLocal(descVal) {
		add("description is missing")
	} else if !descIsString {
		add("description must be a string")
	} else if strings.TrimSpace(descStr) == "" {
		add("description cannot be empty")
	}
	if !jsFalsyLocal(descVal) && descIsString && len(descStr) > 1024 {
		add(fmt.Sprintf("description exceeds 1024 characters (%d chars)", len(descStr)))
	}

	if sevVal, present := g["severity"]; present && sevVal != nil {
		sevStr, isStr := sevVal.(string)
		if !isStr || !containsStr(dimensions.GuardrailSeverities, sevStr) {
			got := sevStr
			if !isStr {
				got = fmt.Sprint(sevVal)
			}
			add(fmt.Sprintf("severity must be %s, or undefined (got %q)", quotedList(dimensions.GuardrailSeverities), got))
		}
	}

	return findings
}

// validateGuardrailsAction ports validateGuardrailsConfig. A missing or
// absent guardrails section is treated as zero guardrails configured (a
// pass), matching the JS "!sectionData || !Array.isArray(...)" branch.
func validateGuardrailsAction(root string, in ValidateIn) ([]discovery.Finding, error) {
	section := in.Section
	if section == "" {
		section = "plan"
	}
	data, err := config.ReadSection(root, section)
	if err != nil {
		if errors.Is(err, config.ErrNotFound) {
			return nil, nil
		}
		return nil, &mcpserver.InfraError{Msg: fmt.Sprintf("read %s guardrails section: %s", section, err.Error()), Cause: err}
	}

	raw, ok := data["guardrails"].([]any)
	if !ok {
		return nil, nil
	}

	seen := map[string]bool{}
	var findings []discovery.Finding
	for _, item := range raw {
		g, ok := item.(map[string]any)
		if !ok {
			continue
		}
		findings = append(findings, validateOneGuardrail(g, seen)...)
	}
	return findings, nil
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
// Exported wrappers (Task 38) -- internal/hooks needs to call these three
// validators directly (post-tool-validate.js's in-process port), but Go
// visibility makes the unexported functions above uncallable cross-package.
// Each wrapper is a pure one-line delegate; the unexported functions and the
// validate dispatcher above are unchanged.
// ---------------------------------------------------------------------------

// ValidatePlanFormat delegates to validatePlanFormat for internal/hooks.
func ValidatePlanFormat(root string, in ValidateIn) ([]discovery.Finding, error) {
	return validatePlanFormat(root, in)
}

// ValidatePRTemplate delegates to validatePRTemplate for internal/hooks.
func ValidatePRTemplate(root string) ([]discovery.Finding, error) {
	return validatePRTemplate(root)
}

// ValidateDimensionsAction delegates to validateDimensionsAction for internal/hooks.
func ValidateDimensionsAction(root string) ([]discovery.Finding, error) {
	return validateDimensionsAction(root)
}

func validateDimensionsAction(root string) ([]discovery.Finding, error) {
	dir := filepath.Join(root, ".sdlc", "review-dimensions")
	dims, err := dimensions.Load(dir)
	if err != nil {
		return nil, &mcpserver.InfraError{Msg: fmt.Sprintf("load review dimensions: %s", err.Error()), Cause: err}
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
