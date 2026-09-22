package tools

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/rnagrodzki/sdlc-plugin/internal/discovery"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
)

// ---------------------------------------------------------------------------
// test helpers
// ---------------------------------------------------------------------------

// writeFile is defined in review_test.go (shared package-level test helper).

func findingsByID(findings []discovery.Finding, id string) []discovery.Finding {
	var out []discovery.Finding
	for _, f := range findings {
		if f.ID == id {
			out = append(out, f)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// plan_format
// ---------------------------------------------------------------------------

const goodPlan = `**Goal:** Do the thing
**Architecture:** Some arch
**Source:** Some source
**Verification:** Some verification

### Task 1: First task
**Complexity:** Standard
**Risk:** Low
**Depends on:** none
**Verify:** tests

- Create: foo.go
**Contract:**
- shape: does X
- names: Foo
- mirror: existing pattern in bar.go
- decisions: none
- sync: none

**Acceptance criteria:**
- [ ] it works

### Task 2: Second task
**Complexity:** Trivial
**Risk:** Low
**Depends on:** Task 1
**Verify:** build, lint

**Acceptance criteria:**
- [ ] works too

**Notes:**
- one note line

## Deviations & assumptions

None.

## Verification Scorecard

All good.
`

const goodTemplate = `# Plan Template

## Required Sections

- Deviations & assumptions
- Verification Scorecard
`

func TestValidatePlanFormatAllChecksPass(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "plan.md"), goodPlan)

	findingsOut, err := validate(root, ValidateIn{Action: "plan_format", File: "plan.md"})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	findings := findingsOut.Findings
	if len(findings) != 0 {
		t.Fatalf("expected 0 findings on well-formed plan, got %d: %+v", len(findings), findings)
	}
}

func TestValidatePlanFormatFinalWithTemplatePasses(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "plan.md"), goodPlan)
	writeFile(t, filepath.Join(root, "template.md"), goodTemplate)

	findingsOut, err := validate(root, ValidateIn{Action: "plan_format", File: "plan.md", Final: true, Template: "template.md"})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	findings := findingsOut.Findings
	if len(findings) != 0 {
		t.Fatalf("expected 0 findings in final mode with matching template, got %d: %+v", len(findings), findings)
	}
}

func TestValidatePlanFormatPF1MissingHeaderField(t *testing.T) {
	root := t.TempDir()
	plan := strings.Replace(goodPlan, "**Goal:** Do the thing\n", "", 1)
	writeFile(t, filepath.Join(root, "plan.md"), plan)

	findingsOut, err := validate(root, ValidateIn{Action: "plan_format", File: "plan.md"})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	findings := findingsOut.Findings
	pf1 := findingsByID(findings, "PF1")
	if len(pf1) != 1 {
		t.Fatalf("expected 1 PF1 finding, got %d: %+v", len(pf1), findings)
	}
	if !strings.Contains(pf1[0].Message, "Goal") {
		t.Errorf("PF1 message %q should mention Goal", pf1[0].Message)
	}
}

func TestValidatePlanFormatPF2NumberingGap(t *testing.T) {
	root := t.TempDir()
	plan := strings.Replace(goodPlan, "### Task 2:", "### Task 3:", 1)
	plan = strings.Replace(plan, "**Depends on:** Task 1", "**Depends on:** none", 1)
	writeFile(t, filepath.Join(root, "plan.md"), plan)

	findingsOut, err := validate(root, ValidateIn{Action: "plan_format", File: "plan.md"})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	findings := findingsOut.Findings
	pf2 := findingsByID(findings, "PF2")
	if len(pf2) != 1 {
		t.Fatalf("expected 1 PF2 finding, got %d: %+v", len(pf2), findings)
	}
	if !strings.Contains(pf2[0].Message, "gap between Task 1 and Task 3") {
		t.Errorf("PF2 message = %q, want gap between Task 1 and Task 3", pf2[0].Message)
	}
}

func TestValidatePlanFormatPF3InvalidComplexity(t *testing.T) {
	root := t.TempDir()
	plan := strings.Replace(goodPlan, "**Complexity:** Standard", "**Complexity:** Bogus", 1)
	writeFile(t, filepath.Join(root, "plan.md"), plan)

	findingsOut, err := validate(root, ValidateIn{Action: "plan_format", File: "plan.md"})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	findings := findingsOut.Findings
	pf3 := findingsByID(findings, "PF3")
	if len(pf3) != 1 {
		t.Fatalf("expected 1 PF3 finding, got %d: %+v", len(pf3), findings)
	}
	if !strings.Contains(pf3[0].Message, `invalid Complexity "Bogus"`) {
		t.Errorf("PF3 message = %q, want invalid Complexity mention", pf3[0].Message)
	}
}

func TestValidatePlanFormatPF4CircularDependency(t *testing.T) {
	root := t.TempDir()
	plan := strings.Replace(goodPlan, "**Depends on:** none", "**Depends on:** Task 2", 1)
	writeFile(t, filepath.Join(root, "plan.md"), plan)

	findingsOut, err := validate(root, ValidateIn{Action: "plan_format", File: "plan.md"})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	findings := findingsOut.Findings
	pf4 := findingsByID(findings, "PF4")
	if len(pf4) != 1 {
		t.Fatalf("expected 1 PF4 finding, got %d: %+v", len(pf4), findings)
	}
	if !strings.Contains(pf4[0].Message, "Circular dependency") {
		t.Errorf("PF4 message = %q, want Circular dependency", pf4[0].Message)
	}
}

func TestValidatePlanFormatPF5MissingAcceptanceCriteria(t *testing.T) {
	root := t.TempDir()
	plan := strings.Replace(goodPlan, "**Acceptance criteria:**\n- [ ] it works\n", "", 1)
	writeFile(t, filepath.Join(root, "plan.md"), plan)

	findingsOut, err := validate(root, ValidateIn{Action: "plan_format", File: "plan.md"})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	findings := findingsOut.Findings
	pf5 := findingsByID(findings, "PF5")
	if len(pf5) != 1 {
		t.Fatalf("expected 1 PF5 finding, got %d: %+v", len(pf5), findings)
	}
	if !strings.Contains(pf5[0].Message, "missing **Acceptance criteria:**") {
		t.Errorf("PF5 message = %q", pf5[0].Message)
	}
}

func TestValidatePlanFormatPF6MissingDeviations(t *testing.T) {
	root := t.TempDir()
	plan := strings.Replace(goodPlan, "## Deviations & assumptions\n\nNone.\n\n", "", 1)
	writeFile(t, filepath.Join(root, "plan.md"), plan)

	findingsOut, err := validate(root, ValidateIn{Action: "plan_format", File: "plan.md"})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	findings := findingsOut.Findings
	pf6 := findingsByID(findings, "PF6")
	if len(pf6) != 1 {
		t.Fatalf("expected 1 PF6 finding, got %d: %+v", len(pf6), findings)
	}
}

func TestValidatePlanFormatPF7MissingContract(t *testing.T) {
	root := t.TempDir()
	plan := strings.Replace(goodPlan, "**Contract:**\n- shape: does X\n- names: Foo\n- mirror: existing pattern in bar.go\n- decisions: none\n- sync: none\n", "", 1)
	writeFile(t, filepath.Join(root, "plan.md"), plan)

	findingsOut, err := validate(root, ValidateIn{Action: "plan_format", File: "plan.md"})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	findings := findingsOut.Findings
	pf7 := findingsByID(findings, "PF7")
	if len(pf7) != 1 {
		t.Fatalf("expected 1 PF7 finding, got %d: %+v", len(pf7), findings)
	}
	if !strings.Contains(pf7[0].Message, "Task 1") {
		t.Errorf("PF7 message = %q, want Task 1", pf7[0].Message)
	}
}

func TestValidatePlanFormatPF9MissingScorecardInFinalMode(t *testing.T) {
	root := t.TempDir()
	plan := strings.Replace(goodPlan, "## Verification Scorecard\n\nAll good.\n", "", 1)
	writeFile(t, filepath.Join(root, "plan.md"), plan)

	findingsOut, err := validate(root, ValidateIn{Action: "plan_format", File: "plan.md", Final: true})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	findings := findingsOut.Findings
	pf9 := findingsByID(findings, "PF9")
	if len(pf9) != 1 {
		t.Fatalf("expected 1 PF9 finding, got %d: %+v", len(pf9), findings)
	}

	// PF9 must NOT fire in non-final mode.
	findingsNonFinalOut, err := validate(root, ValidateIn{Action: "plan_format", File: "plan.md"})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	findingsNonFinal := findingsNonFinalOut.Findings
	if len(findingsByID(findingsNonFinal, "PF9")) != 0 {
		t.Errorf("PF9 should not run outside --final mode")
	}
}

func TestValidatePlanFormatPF10MissingTemplateSection(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "plan.md"), goodPlan)
	badTemplate := strings.Replace(goodTemplate, "- Verification Scorecard\n", "- Verification Scorecard\n- Rollback Plan\n", 1)
	writeFile(t, filepath.Join(root, "template.md"), badTemplate)

	findingsOut, err := validate(root, ValidateIn{Action: "plan_format", File: "plan.md", Final: true, Template: "template.md"})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	findings := findingsOut.Findings
	pf10 := findingsByID(findings, "PF10")
	if len(pf10) != 1 {
		t.Fatalf("expected 1 PF10 finding, got %d: %+v", len(pf10), findings)
	}
	if !strings.Contains(pf10[0].Message, "Rollback Plan") {
		t.Errorf("PF10 message = %q, want Rollback Plan", pf10[0].Message)
	}
}

func TestValidatePlanFormatFileNotFound(t *testing.T) {
	root := t.TempDir()
	_, err := validate(root, ValidateIn{Action: "plan_format", File: "missing.md"})
	if err == nil {
		t.Fatal("expected error for missing plan file")
	}
}

// ---------------------------------------------------------------------------
// discovery
// ---------------------------------------------------------------------------

func TestValidateDiscoveryDelegatesToDiscoveryPackage(t *testing.T) {
	root := t.TempDir()
	// An empty project root: discovery.ValidateAll should run without
	// error and return whatever it returns for a bare directory. We only
	// assert the action wires through without erroring.
	out, err := validate(root, ValidateIn{Action: "discovery"})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	_ = out
}

// ---------------------------------------------------------------------------
// pr_template
// ---------------------------------------------------------------------------

func TestValidatePRTemplateAllChecksPass(t *testing.T) {
	root := t.TempDir()
	tmpl := "## Description\n\nThis section has more than twenty characters in it.\n\n## Testing\n\nAlso has enough characters here to pass.\n"
	writeFile(t, filepath.Join(root, paths.DataDir, "pr-template.md"), tmpl)

	findingsOut, err := validate(root, ValidateIn{Action: "pr_template"})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	findings := findingsOut.Findings
	if len(findings) != 0 {
		t.Fatalf("expected 0 findings, got %+v", findings)
	}
}

func TestValidatePRTemplateV1FileNotFound(t *testing.T) {
	root := t.TempDir()
	findingsOut, err := validate(root, ValidateIn{Action: "pr_template"})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	findings := findingsOut.Findings
	v1 := findingsByID(findings, "V1")
	if len(v1) != 1 {
		t.Fatalf("expected 1 V1 finding, got %+v", findings)
	}
}

func TestValidatePRTemplateV2Empty(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, paths.DataDir, "pr-template.md"), "   \n\n  ")
	findingsOut, err := validate(root, ValidateIn{Action: "pr_template"})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	findings := findingsOut.Findings
	if len(findingsByID(findings, "V2")) != 1 {
		t.Fatalf("expected 1 V2 finding, got %+v", findings)
	}
}

func TestValidatePRTemplateV3NoHeadings(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, paths.DataDir, "pr-template.md"), "just some text, no headings at all")
	findingsOut, err := validate(root, ValidateIn{Action: "pr_template"})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	findings := findingsOut.Findings
	if len(findingsByID(findings, "V3")) != 1 {
		t.Fatalf("expected 1 V3 finding, got %+v", findings)
	}
}

func TestValidatePRTemplateV4DuplicateHeadings(t *testing.T) {
	root := t.TempDir()
	tmpl := "## Description\n\nThis section has more than twenty characters.\n\n## description\n\nAnother section long enough too.\n"
	writeFile(t, filepath.Join(root, paths.DataDir, "pr-template.md"), tmpl)
	findingsOut, err := validate(root, ValidateIn{Action: "pr_template"})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	findings := findingsOut.Findings
	v4 := findingsByID(findings, "V4")
	if len(v4) != 1 {
		t.Fatalf("expected 1 V4 finding, got %+v", findings)
	}
	if !strings.Contains(v4[0].Message, "appears 2 times") {
		t.Errorf("V4 message = %q", v4[0].Message)
	}
	// V5 must still run even when V4 fails.
}

func TestValidatePRTemplateV5ShortSection(t *testing.T) {
	root := t.TempDir()
	tmpl := "## Description\n\nshort\n"
	writeFile(t, filepath.Join(root, paths.DataDir, "pr-template.md"), tmpl)
	findingsOut, err := validate(root, ValidateIn{Action: "pr_template"})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	findings := findingsOut.Findings
	if len(findingsByID(findings, "V5")) != 1 {
		t.Fatalf("expected 1 V5 finding, got %+v", findings)
	}
}

// ---------------------------------------------------------------------------
// pr_body
// ---------------------------------------------------------------------------

func TestValidatePRBodyNoTemplateAlwaysPasses(t *testing.T) {
	root := t.TempDir()
	findingsOut, err := validate(root, ValidateIn{Action: "pr_body", Body: "anything at all"})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if len(findingsOut.Findings) != 0 {
		t.Fatalf("expected 0 findings when no template exists, got %+v", findingsOut.Findings)
	}
}

func TestValidatePRBodyMissingSectionsProducesFindings(t *testing.T) {
	root := t.TempDir()
	tmpl := "## Summary\n<!-- what changed -->\n\n## Testing\n<!-- how verified -->\n"
	writeFile(t, filepath.Join(root, paths.DataDir, "pr-template.md"), tmpl)

	findingsOut, err := validate(root, ValidateIn{Action: "pr_body", Body: "## Summary\nDid the thing.\n"})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	findings := findingsOut.Findings
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding for one missing section, got %d: %+v", len(findings), findings)
	}
	if findings[0].ID != "PR_BODY" || findings[0].Severity != "error" {
		t.Errorf("finding = %+v, want ID=PR_BODY Severity=error", findings[0])
	}
}

func TestValidatePRBodyAllSectionsPresentPasses(t *testing.T) {
	root := t.TempDir()
	tmpl := "## Summary\n<!-- what changed -->\n\n## Testing\n<!-- how verified -->\n"
	writeFile(t, filepath.Join(root, paths.DataDir, "pr-template.md"), tmpl)

	findingsOut, err := validate(root, ValidateIn{
		Action: "pr_body",
		Body:   "## Summary\nDid the thing.\n\n## Testing\nRan tests.\n",
	})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if len(findingsOut.Findings) != 0 {
		t.Fatalf("expected 0 findings, got %+v", findingsOut.Findings)
	}
}

// ---------------------------------------------------------------------------
// cost_tiers
// ---------------------------------------------------------------------------

func writeSkill(t *testing.T, root, name, model string) {
	t.Helper()
	fm := "---\nname: " + name + "\ndescription: a skill\n"
	if model != "" {
		fm += "model: " + model + "\n"
	}
	fm += "---\nBody.\n"
	writeFile(t, filepath.Join(root, "skills", name, "SKILL.md"), fm)
}

func writeAgent(t *testing.T, root, name, model string) {
	t.Helper()
	fm := "---\nname: " + name + "\ndescription: an agent\n"
	if model != "" {
		fm += "model: " + model + "\n"
	}
	fm += "---\nBody.\n"
	writeFile(t, filepath.Join(root, "agents", name+".md"), fm)
}

func writeCostTiersDoc(t *testing.T, root string, skillRows, agentRows [][2]string) {
	t.Helper()
	var b strings.Builder
	b.WriteString("## 3. Skill Table\n\n| Name | Model |\n| --- | --- |\n")
	for _, r := range skillRows {
		b.WriteString("| " + r[0] + " | " + r[1] + " |\n")
	}
	b.WriteString("\n## 4. Agent Table\n\n| Name | Model |\n| --- | --- |\n")
	for _, r := range agentRows {
		b.WriteString("| " + r[0] + " | " + r[1] + " |\n")
	}
	writeFile(t, filepath.Join(root, "docs", "cost-tiers.md"), b.String())
}

func TestValidateCostTiersAllKinds(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "clean-skill", "opus")
	writeSkill(t, root, "drift-skill", "haiku")
	writeSkill(t, root, "missing-doc-skill", "sonnet")
	writeSkill(t, root, "inherited-skill", "")
	writeAgent(t, root, "clean-agent", "sonnet")

	writeCostTiersDoc(t, root,
		[][2]string{
			{"clean-skill", "opus"},
			{"drift-skill", "opus"},
			{"stale-skill", "haiku"},
		},
		[][2]string{
			{"clean-agent", "sonnet"},
		},
	)

	findingsOut, err := validate(root, ValidateIn{Action: "cost_tiers"})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	findings := findingsOut.Findings

	drift := findingsByID(findings, "DRIFT")
	missingDoc := findingsByID(findings, "MISSING_DOC")
	staleDoc := findingsByID(findings, "STALE_DOC")
	inherited := findingsByID(findings, "INHERITED")

	if len(drift) != 1 {
		t.Errorf("expected 1 DRIFT finding, got %d: %+v", len(drift), drift)
	}
	if len(missingDoc) != 1 {
		t.Errorf("expected 1 MISSING_DOC finding, got %d: %+v", len(missingDoc), missingDoc)
	}
	if len(staleDoc) != 1 {
		t.Errorf("expected 1 STALE_DOC finding, got %d: %+v", len(staleDoc), staleDoc)
	}
	if len(inherited) != 1 {
		t.Fatalf("expected 1 INHERITED finding, got %d: %+v", len(inherited), inherited)
	}
	if inherited[0].Severity != "warning" {
		t.Errorf("INHERITED severity = %q, want warning when Strict=false", inherited[0].Severity)
	}

	// Strict=true should escalate INHERITED to "error".
	findingsStrictOut, err := validate(root, ValidateIn{Action: "cost_tiers", Strict: true})
	if err != nil {
		t.Fatalf("validate strict: %v", err)
	}
	findingsStrict := findingsStrictOut.Findings
	inheritedStrict := findingsByID(findingsStrict, "INHERITED")
	if len(inheritedStrict) != 1 || inheritedStrict[0].Severity != "error" {
		t.Errorf("expected INHERITED severity=error under Strict=true, got %+v", inheritedStrict)
	}
}

func TestValidateCostTiersDocMissing(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "some-skill", "opus")
	_, err := validate(root, ValidateIn{Action: "cost_tiers"})
	if err == nil {
		t.Fatal("expected error when docs/cost-tiers.md is missing")
	}
}

// ---------------------------------------------------------------------------
// guardrails
// ---------------------------------------------------------------------------

// TestValidateGuardrailsAllChecks exercises validateOneGuardrail's checks via
// a real config.toml. Guardrails are keyed named tables
// ([plan.guardrails.<id>]); config.ReadSection always injects "id" from the
// table key (see normalizeGuardrailTables/guardrailsTableToSlice in
// internal/config/config.go), which makes two of the original JSON fixture's
// cases structurally unrepresentable here and they are intentionally
// dropped:
//   - a guardrail with no "id" at all: every table key becomes a non-empty
//     id, so the id-is-missing branch of validateOneGuardrail can no longer
//     be reached through a config file.
//   - two guardrails sharing one "id" (duplicate detection): TOML tables
//     cannot repeat the same key ([plan.guardrails.dup-id] twice is a parse
//     error), so the duplicate-id branch can no longer be reached through a
//     config file either.
//
// Both branches are still reachable in principle if validateOneGuardrail is
// ever called directly or fed a hand-rolled []any (e.g. a non-canonical
// [[plan.guardrails]] array-of-tables with an explicit "id" field, which
// normalizeGuardrailTables does not touch), but no test exercises that path
// post-migration. Flagged as a coverage reduction, not fixed here.
func TestValidateGuardrailsAllChecks(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, paths.DataDir, "config.toml"), ""+
		"[plan.guardrails.good-guardrail]\n"+
		"description = \"A valid guardrail description.\"\n"+
		"\n"+
		"[plan.guardrails.Bad_ID]\n"+
		"description = \"desc\"\n"+
		"\n"+
		"[plan.guardrails.sev-bad]\n"+
		"description = \"d3\"\n"+
		"severity = \"critical\"\n"+
		"\n"+
		"[plan.guardrails.no-desc]\n")

	findingsOut, err := validate(root, ValidateIn{Action: "guardrails"})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	findings := findingsOut.Findings
	if len(findings) != 3 {
		t.Fatalf("expected 3 guardrail findings, got %d: %+v", len(findings), findings)
	}
	for _, f := range findings {
		if f.Severity != "error" {
			t.Errorf("guardrail finding severity = %q, want error: %+v", f.Severity, f)
		}
	}

	byID := map[string]int{}
	for _, f := range findings {
		byID[f.ID]++
	}
	if byID["Bad_ID"] != 1 {
		t.Errorf("expected 1 finding for Bad_ID, got %d", byID["Bad_ID"])
	}
	if byID["sev-bad"] != 1 {
		t.Errorf("expected 1 finding for sev-bad, got %d", byID["sev-bad"])
	}
	if byID["no-desc"] != 1 {
		t.Errorf("expected 1 finding for no-desc, got %d", byID["no-desc"])
	}
	if byID["good-guardrail"] != 0 {
		t.Errorf("expected 0 findings for good-guardrail, got %d", byID["good-guardrail"])
	}
}

func TestValidateGuardrailsNoSectionIsPass(t *testing.T) {
	root := t.TempDir() // no .sdlc-v2/config.toml at all
	findingsOut, err := validate(root, ValidateIn{Action: "guardrails"})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	findings := findingsOut.Findings
	if len(findings) != 0 {
		t.Fatalf("expected 0 findings with no config, got %+v", findings)
	}
}

func TestValidateGuardrailsEmptySectionIsPass(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, paths.DataDir, "config.toml"), "[plan]\n")
	findingsOut, err := validate(root, ValidateIn{Action: "guardrails"})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	findings := findingsOut.Findings
	if len(findings) != 0 {
		t.Fatalf("expected 0 findings with empty plan section, got %+v", findings)
	}
}

func TestValidateGuardrailsCustomSection(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, paths.DataDir, "config.toml"), ""+
		"[execute.guardrails.exec-guardrail]\n"+
		"description = \"\"\n")
	findingsOut, err := validate(root, ValidateIn{Action: "guardrails", Section: "execute"})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	findings := findingsOut.Findings
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding (empty description) from execute section, got %+v", findings)
	}
}

// ---------------------------------------------------------------------------
// dimensions
// ---------------------------------------------------------------------------

func TestValidateDimensionsValidFileHasNoFindings(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, paths.DataDir, "review-dimensions", "a-dim.md"), "---\n"+
		"name: security-review\n"+
		"description: Check for security issues in the diff.\n"+
		"triggers:\n"+
		"  - \"**/*.go\"\n"+
		"---\n"+
		"This dimension reviews code for security vulnerabilities and unsafe patterns.\n")

	findingsOut, err := validate(root, ValidateIn{Action: "dimensions"})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	findings := findingsOut.Findings
	if len(findings) != 0 {
		t.Fatalf("expected 0 findings for a valid dimension file, got %+v", findings)
	}
}

func TestValidateDimensionsMissingFieldAndUnknownField(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, paths.DataDir, "review-dimensions", "bad-dim.md"), "---\n"+
		"name: bad-dim\n"+
		"triggers:\n"+
		"  - \"**/*.js\"\n"+
		"randomfield: true\n"+
		"---\n"+
		"Body text here that is long enough to pass the D9 body-length check.\n")

	findingsOut, err := validate(root, ValidateIn{Action: "dimensions"})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	findings := findingsOut.Findings

	d3 := findingsByID(findings, "D3")
	if len(d3) != 1 || d3[0].Message != "Missing required field: description" {
		t.Errorf("expected 1 D3 finding with exact message, got %+v", d3)
	}
	if d3[0].Severity != "error" {
		t.Errorf("D3 severity = %q, want error", d3[0].Severity)
	}

	d11 := findingsByID(findings, "D11")
	if len(d11) != 1 {
		t.Fatalf("expected 1 D11 finding, got %+v", d11)
	}
	if d11[0].Severity != "warning" {
		t.Errorf("D11 severity = %q, want warning", d11[0].Severity)
	}
	if !strings.Contains(d11[0].Message, `"randomfield"`) {
		t.Errorf("D11 message = %q, want mention of randomfield", d11[0].Message)
	}
}

func TestValidateDimensionsD10DuplicateName(t *testing.T) {
	root := t.TempDir()
	dim := "---\n" +
		"name: security-review\n" +
		"description: Check for security issues in the diff.\n" +
		"triggers:\n" +
		"  - \"**/*.go\"\n" +
		"---\n" +
		"This dimension reviews code for security vulnerabilities and unsafe patterns.\n"
	writeFile(t, filepath.Join(root, paths.DataDir, "review-dimensions", "a-dim.md"), dim)
	writeFile(t, filepath.Join(root, paths.DataDir, "review-dimensions", "b-dim.md"), dim)

	findingsOut, err := validate(root, ValidateIn{Action: "dimensions"})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	findings := findingsOut.Findings
	d10 := findingsByID(findings, "D10")
	if len(d10) != 1 {
		t.Fatalf("expected 1 D10 finding, got %+v", findings)
	}
	want := `Duplicate dimension name "security-review" — also used in a-dim.md`
	if d10[0].Message != want {
		t.Errorf("D10 message = %q, want %q", d10[0].Message, want)
	}
	if d10[0].Path != "b-dim.md" {
		t.Errorf("D10 path = %q, want b-dim.md", d10[0].Path)
	}
}

// ---------------------------------------------------------------------------
// unknown action
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// ci_script_drift
// ---------------------------------------------------------------------------

func TestValidateCIScriptDrift_AllCurrentIsPass(t *testing.T) {
	root := t.TempDir()
	if _, err := scaffoldCI(root, false); err != nil {
		t.Fatalf("scaffoldCI: %v", err)
	}

	out, err := validate(root, ValidateIn{Action: "ci_script_drift"})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if len(out.Findings) != 0 {
		t.Errorf("expected no findings for a freshly scaffolded project, got %v", out.Findings)
	}
}

func TestValidateCIScriptDrift_MissingAndOutdatedProduceDistinctFindings(t *testing.T) {
	root := t.TempDir()
	if _, err := scaffoldCI(root, false); err != nil {
		t.Fatalf("scaffoldCI: %v", err)
	}

	// Downgrade one script, delete another entirely.
	if err := os.WriteFile(filepath.Join(root, ".github", "workflows", "retag-release.yml"), []byte("# retag-release-version: 1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, ".github", "workflows", "check-changelog.yml")); err != nil {
		t.Fatal(err)
	}

	out, err := validate(root, ValidateIn{Action: "ci_script_drift"})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}

	outdated := findingsByID(out.Findings, "CI_SCRIPT_OUTDATED")
	if len(outdated) != 1 {
		t.Fatalf("expected 1 CI_SCRIPT_OUTDATED finding, got %d: %v", len(outdated), out.Findings)
	}
	if !strings.Contains(outdated[0].Message, "scaffold_ci({force:true})") {
		t.Errorf("outdated finding message missing remediation hint: %s", outdated[0].Message)
	}

	missing := findingsByID(out.Findings, "CI_SCRIPT_MISSING")
	if len(missing) != 1 {
		t.Fatalf("expected 1 CI_SCRIPT_MISSING finding, got %d: %v", len(missing), out.Findings)
	}
	if !strings.Contains(missing[0].Message, "scaffold_ci({force:true})") {
		t.Errorf("missing finding message missing remediation hint: %s", missing[0].Message)
	}
}

func TestValidateUnknownAction(t *testing.T) {
	root := t.TempDir()
	_, err := validate(root, ValidateIn{Action: "nonsense"})
	if err == nil {
		t.Fatal("expected error for unknown action")
	}
}

// ---------------------------------------------------------------------------
// links_validate
// ---------------------------------------------------------------------------

func TestLinksValidateExtractsAndZipsLines(t *testing.T) {
	root := t.TempDir()
	content := "See https://example.com/foo, and also https://example.com/foo again.\n" +
		"Also (https://example.com/bar) in parens.\n" +
		"Line3 https://example.com/baz.\n"
	writeFile(t, filepath.Join(root, "doc.md"), content)

	out, err := linksValidate(root, LinksValidateIn{File: "doc.md", Offline: true})
	if err != nil {
		t.Fatalf("linksValidate: %v", err)
	}
	if len(out.Results) != 3 {
		t.Fatalf("expected 3 distinct URLs, got %d: %+v", len(out.Results), out.Results)
	}

	want := []struct {
		url  string
		line int
	}{
		{"https://example.com/foo", 1},
		{"https://example.com/bar", 2},
		{"https://example.com/baz", 3},
	}
	for i, w := range want {
		r := out.Results[i]
		if r.URL != w.url {
			t.Errorf("result[%d].URL = %q, want %q", i, r.URL, w.url)
		}
		if r.Line != w.line {
			t.Errorf("result[%d].Line = %d, want %d", i, r.Line, w.line)
		}
		if r.Status != "skipped" || r.Reason != "offline" {
			t.Errorf("result[%d] = {status:%q reason:%q}, want skipped/offline", i, r.Status, r.Reason)
		}
	}
}

func TestLinksValidateFileNotFound(t *testing.T) {
	root := t.TempDir()
	_, err := linksValidate(root, LinksValidateIn{File: "missing.md"})
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

// ---------------------------------------------------------------------------
// mcp_failure_record
// ---------------------------------------------------------------------------

func TestMcpFailureRecordClassifiesAndRecords(t *testing.T) {
	root := t.TempDir()

	out, err := mcpFailureRecord(root, MCPFailureRecordIn{
		Tool:         "test_tool",
		HTTPStatus:   401,
		ErrorMessage: "unauthorized access",
	})
	if err != nil {
		t.Fatalf("mcpFailureRecord: %v", err)
	}
	if out.Class != "auth" {
		t.Errorf("Class = %q, want auth", out.Class)
	}
	if !out.Recorded {
		t.Errorf("Recorded = false, want true")
	}
	if out.SessionID == "" {
		t.Errorf("SessionID should not be empty")
	}

	logPath := filepath.Join(root, paths.DataDir, "learnings", "log.md")
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log.md: %v", err)
	}
	if !strings.Contains(string(data), "mcp-failure[auth]: test_tool") {
		t.Errorf("log.md missing expected heading, got: %s", data)
	}

	firstLen := len(data)

	// Second identical call on the same day must be a dedup no-op.
	if _, err := mcpFailureRecord(root, MCPFailureRecordIn{
		Tool:         "test_tool",
		HTTPStatus:   401,
		ErrorMessage: "unauthorized access",
	}); err != nil {
		t.Fatalf("mcpFailureRecord (2nd): %v", err)
	}
	data2, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log.md (2nd): %v", err)
	}
	if len(data2) != firstLen {
		t.Errorf("expected idempotent no-op on duplicate failure, log.md grew from %d to %d bytes", firstLen, len(data2))
	}
}

func TestMcpFailureRecordRequiresTool(t *testing.T) {
	root := t.TempDir()
	_, err := mcpFailureRecord(root, MCPFailureRecordIn{})
	if err == nil {
		t.Fatal("expected error when tool is empty")
	}
}

// ---------------------------------------------------------------------------
// parseTemplateRequiredSectionsFull (Task 3) & PF11/PF12 (Task 7)
// ---------------------------------------------------------------------------

func TestParseTemplateRequiredSectionsFull(t *testing.T) {
	t.Run("ParseFull_Narrative", func(t *testing.T) {
		root := t.TempDir()
		path := filepath.Join(root, "template.md")
		writeFile(t, path, "# Plan Template\n\n## Required Sections\n\n- Context <!-- narrative: true -->\n")

		sections, err := parseTemplateRequiredSectionsFull(path)
		if err != nil {
			t.Fatalf("parseTemplateRequiredSectionsFull: %v", err)
		}
		if len(sections) != 1 {
			t.Fatalf("expected 1 section, got %d: %+v", len(sections), sections)
		}
		if sections[0].Name != "Context" {
			t.Errorf("Name = %q, want Context", sections[0].Name)
		}
		if !sections[0].Narrative {
			t.Errorf("Narrative = false, want true")
		}
		if sections[0].Condition != nil {
			t.Errorf("Condition = %v, want nil", sections[0].Condition)
		}
	})

	t.Run("ParseFull_Conditional", func(t *testing.T) {
		root := t.TempDir()
		path := filepath.Join(root, "template.md")
		writeFile(t, path, "# Plan Template\n\n## Required Sections\n\n- OpenSpec <!-- conditional: X -->\n")

		sections, err := parseTemplateRequiredSectionsFull(path)
		if err != nil {
			t.Fatalf("parseTemplateRequiredSectionsFull: %v", err)
		}
		if len(sections) != 1 {
			t.Fatalf("expected 1 section, got %d: %+v", len(sections), sections)
		}
		if sections[0].Name != "OpenSpec" {
			t.Errorf("Name = %q, want OpenSpec", sections[0].Name)
		}
		if sections[0].Narrative {
			t.Errorf("Narrative = true, want false")
		}
		if sections[0].Condition == nil || *sections[0].Condition != "X" {
			t.Errorf("Condition = %v, want X", sections[0].Condition)
		}
	})

	t.Run("ParseFull_Both", func(t *testing.T) {
		root := t.TempDir()
		path := filepath.Join(root, "template.md")
		writeFile(t, path, "# Plan Template\n\n## Required Sections\n\n- Foo <!-- narrative: true --> <!-- conditional: Y -->\n")

		sections, err := parseTemplateRequiredSectionsFull(path)
		if err != nil {
			t.Fatalf("parseTemplateRequiredSectionsFull: %v", err)
		}
		if len(sections) != 1 {
			t.Fatalf("expected 1 section, got %d: %+v", len(sections), sections)
		}
		if sections[0].Name != "Foo" {
			t.Errorf("Name = %q, want Foo", sections[0].Name)
		}
		if !sections[0].Narrative {
			t.Errorf("Narrative = false, want true")
		}
		if sections[0].Condition == nil || *sections[0].Condition != "Y" {
			t.Errorf("Condition = %v, want Y", sections[0].Condition)
		}
	})

	t.Run("ParseFull_Plain", func(t *testing.T) {
		root := t.TempDir()
		path := filepath.Join(root, "template.md")
		writeFile(t, path, "# Plan Template\n\n## Required Sections\n\n- Research Findings\n")

		sections, err := parseTemplateRequiredSectionsFull(path)
		if err != nil {
			t.Fatalf("parseTemplateRequiredSectionsFull: %v", err)
		}
		if len(sections) != 1 {
			t.Fatalf("expected 1 section, got %d: %+v", len(sections), sections)
		}
		if sections[0].Name != "Research Findings" {
			t.Errorf("Name = %q, want Research Findings", sections[0].Name)
		}
		if sections[0].Narrative {
			t.Errorf("Narrative = true, want false")
		}
		if sections[0].Condition != nil {
			t.Errorf("Condition = %v, want nil", sections[0].Condition)
		}
	})

	t.Run("ParseFull_NoHeading", func(t *testing.T) {
		root := t.TempDir()
		path := filepath.Join(root, "template.md")
		writeFile(t, path, "# Plan Template\n\nJust some prose, no Required Sections heading.\n")

		sections, err := parseTemplateRequiredSectionsFull(path)
		if err != nil {
			t.Fatalf("parseTemplateRequiredSectionsFull: %v", err)
		}
		if sections != nil {
			t.Errorf("expected nil sections, got %+v", sections)
		}
	})
}

func TestValidatePlanFormat_PF11(t *testing.T) {
	t.Run("PF11_Present", func(t *testing.T) {
		tasks := []planTask{{Number: 1, Title: "First", Body: "**Owner:** Alice\n"}}
		result := checkPF11(tasks, []string{"Owner"})
		if result.status != "pass" {
			t.Errorf("status = %q, want pass: %s", result.status, result.message)
		}
	})

	t.Run("PF11_Missing", func(t *testing.T) {
		tasks := []planTask{{Number: 1, Title: "First", Body: "no owner field here\n"}}
		result := checkPF11(tasks, []string{"Owner"})
		if result.status != "fail" {
			t.Fatalf("status = %q, want fail", result.status)
		}
		if !strings.Contains(result.message, "Task 1") {
			t.Errorf("message = %q, want mention of Task 1", result.message)
		}
	})
}

func TestValidatePlanFormat_PF12(t *testing.T) {
	t.Run("PF12_None", func(t *testing.T) {
		// pf7BulletRe-gated task with no Contract block at all; "none" must
		// skip the check entirely, regardless of what's missing.
		tasks := []planTask{{Number: 1, Title: "First", Body: "- Create: foo.go\n"}}
		result := checkPF12(tasks, "none")
		if result.status != "pass" {
			t.Errorf("status = %q, want pass: %s", result.status, result.message)
		}
	})

	t.Run("PF12_Minimal", func(t *testing.T) {
		// Shallow one-line Contract: presence-only check for "minimal" must pass.
		tasks := []planTask{{Number: 1, Title: "First", Body: "- Create: foo.go\n**Contract:** does X\n"}}
		result := checkPF12(tasks, "minimal")
		if result.status != "pass" {
			t.Errorf("status = %q, want pass: %s", result.status, result.message)
		}
	})

	t.Run("PF12_Full_Shallow", func(t *testing.T) {
		// Same shallow one-line Contract, but "full" requires the five keyed
		// bullets (shape/names/mirror/decisions/sync) and must fail.
		tasks := []planTask{{Number: 1, Title: "First", Body: "- Create: foo.go\n**Contract:** does X\n"}}
		result := checkPF12(tasks, "full")
		if result.status != "fail" {
			t.Fatalf("status = %q, want fail", result.status)
		}
		if !strings.Contains(result.message, "Task 1") {
			t.Errorf("message = %q, want mention of Task 1", result.message)
		}
	})
}

// ---------------------------------------------------------------------------
// worktree_anchoring stray-state detection (Task 8)
// ---------------------------------------------------------------------------

func TestFindStrayStateEntries_PlantedFileProducesFinding(t *testing.T) {
	mainRoot := t.TempDir()
	activeRoot := t.TempDir()

	activeDataDir := filepath.Join(activeRoot, paths.DataDir)
	if err := os.MkdirAll(filepath.Join(activeDataDir, "reports"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(activeDataDir, "reports", "x.md"), []byte("stray"), 0o644); err != nil {
		t.Fatal(err)
	}

	findings, err := findStrayStateEntries(mainRoot, activeRoot)
	if err != nil {
		t.Fatalf("findStrayStateEntries: %v", err)
	}
	strayFindings := findingsByID(findings, "WORKTREE_ANCHOR_STRAY_STATE")
	if len(strayFindings) != 1 {
		t.Fatalf("expected 1 WORKTREE_ANCHOR_STRAY_STATE finding, got %d: %+v", len(strayFindings), findings)
	}
	if strayFindings[0].Severity != "error" {
		t.Errorf("severity = %q, want error", strayFindings[0].Severity)
	}
	if !strings.Contains(strayFindings[0].Message, "reports") {
		t.Errorf("message = %q, want mention of the stray entry name", strayFindings[0].Message)
	}
}

func TestFindStrayStateEntries_OnlyTrackedFilesProducesNoFinding(t *testing.T) {
	mainRoot := t.TempDir()
	activeRoot := t.TempDir()

	activeDataDir := filepath.Join(activeRoot, paths.DataDir)
	if err := os.MkdirAll(filepath.Join(activeDataDir, "review-dimensions"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{".gitignore", "config.toml"} {
		if err := os.WriteFile(filepath.Join(activeDataDir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	findings, err := findStrayStateEntries(mainRoot, activeRoot)
	if err != nil {
		t.Fatalf("findStrayStateEntries: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("expected no findings, got %+v", findings)
	}
}

func TestFindStrayStateEntries_MissingStateDirIsNotAnError(t *testing.T) {
	mainRoot := t.TempDir()
	activeRoot := t.TempDir() // no .sdlc-v2/ created at all in the active worktree

	findings, err := findStrayStateEntries(mainRoot, activeRoot)
	if err != nil {
		t.Fatalf("findStrayStateEntries: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("expected no findings, got %+v", findings)
	}
}

// TestValidateCostTiers_ErrorsByCause pins the two recovery paths of
// validateCostTiers: a missing or unreadable docs/cost-tiers.md must not be
// reported as a heading problem, and neither message may print the file path
// twice.
func TestValidateCostTiers_ErrorsByCause(t *testing.T) {
	cases := []struct {
		name        string
		doc         string // "" means: do not create the file
		wantHint    string
		notWantHint string
	}{
		{
			name:        "missing file",
			doc:         "",
			wantHint:    "check read permission",
			notWantHint: "headings",
		},
		{
			name:        "malformed tables",
			doc:         "# Cost tiers\n\nNo tables here.\n",
			wantHint:    "## 3. Skill Table",
			notWantHint: "read permission",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if tc.doc != "" {
				writeFile(t, filepath.Join(root, "docs", "cost-tiers.md"), tc.doc)
			}

			_, err := validateCostTiers(root, ValidateIn{})
			if err == nil {
				t.Fatal("expected an error")
			}
			if got := errorClassOf(err); got != "data" {
				t.Errorf("error class = %q, want data", got)
			}

			var dataErr *mcpserver.DataError
			if !errors.As(err, &dataErr) {
				t.Fatalf("expected *mcpserver.DataError, got %T", err)
			}
			if n := strings.Count(dataErr.Msg, "cost-tiers.md"); n != 1 {
				t.Errorf("Msg names cost-tiers.md %d times, want exactly 1: %s", n, dataErr.Msg)
			}
			if !strings.Contains(dataErr.Suggestion, tc.wantHint) {
				t.Errorf("Suggestion = %q, want it to contain %q", dataErr.Suggestion, tc.wantHint)
			}
			if strings.Contains(dataErr.Suggestion, tc.notWantHint) {
				t.Errorf("Suggestion = %q, must not contain %q", dataErr.Suggestion, tc.notWantHint)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// plan_format: self-contained failure messages (fix text, Verify scope hints)
// ---------------------------------------------------------------------------

func TestValidVerifyValue(t *testing.T) {
	accept := []string{
		"tests",
		"build",
		"lint",
		"manual",
		"tests (go test ./internal/tools/ -run TestFoo)",
		"build (go build ./...)",
		"lint(golangci-lint run ./internal/...)",
		"manual (open the page and check the header)",
		`tests (go test ./... -run "(TestA|TestB)")`,
		"  tests  ",
	}
	for _, v := range accept {
		if !validVerifyValue(v) {
			t.Errorf("validVerifyValue(%q) = false, want true", v)
		}
	}

	reject := []string{
		"",
		"flaky",
		"Tests",
		"tests (",
		"tests (a",
		"tests ()",
		"tests (  )",
		"tests)",
		"tests (a) (b)",
		"testsuite (x)",
		"tests x",
	}
	for _, v := range reject {
		if validVerifyValue(v) {
			t.Errorf("validVerifyValue(%q) = true, want false", v)
		}
	}
}

func TestSplitVerifyValues(t *testing.T) {
	cases := []struct {
		field string
		want  []string
	}{
		{"tests", []string{"tests"}},
		{"tests, build", []string{"tests", "build"}},
		{"tests (go test ./a/ -run A,B), build", []string{"tests (go test ./a/ -run A,B)", "build"}},
		{"tests (a, (b, c)), lint", []string{"tests (a, (b, c))", "lint"}},
		// An unclosed "(" keeps the rest of the field in one value, which
		// validVerifyValue then rejects.
		{"tests (a, b", []string{"tests (a, b"}},
	}
	for _, tc := range cases {
		got := splitVerifyValues(tc.field)
		for i := range got {
			got[i] = strings.TrimSpace(got[i])
		}
		if strings.Join(got, "|") != strings.Join(tc.want, "|") || len(got) != len(tc.want) {
			t.Errorf("splitVerifyValues(%q) = %q, want %q", tc.field, got, tc.want)
		}
	}
}

func TestValidatePlanFormatPF3VerifyScopeHint(t *testing.T) {
	cases := []struct {
		name    string
		verify  string
		wantBad string // quoted value expected in the message; empty means PF3 must pass
	}{
		{"scope hint", "tests (go test ./internal/tools/ -run TestFoo)", ""},
		{"hint with comma, then a second value", "tests (go test ./a/ -run A,B), build", ""},
		{"all four values with hints", "tests (go test ./a/), build (go build ./...), lint (go vet ./...), manual (check the header)", ""},
		{"unclosed paren", "tests (", `"tests ("`},
		{"empty hint", "tests ()", `"tests ()"`},
		{"unknown word", "flaky", `"flaky"`},
		{"one good value, one bad", "tests, flaky", `"flaky"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			plan := strings.Replace(goodPlan, "**Verify:** tests\n", "**Verify:** "+tc.verify+"\n", 1)
			writeFile(t, filepath.Join(root, "plan.md"), plan)

			out, err := validate(root, ValidateIn{Action: "plan_format", File: "plan.md"})
			if err != nil {
				t.Fatalf("validate: %v", err)
			}
			pf3 := findingsByID(out.Findings, "PF3")

			if tc.wantBad == "" {
				if len(pf3) != 0 {
					t.Fatalf("Verify %q should pass PF3, got: %+v", tc.verify, pf3)
				}
				return
			}
			if len(pf3) != 1 {
				t.Fatalf("Verify %q: expected 1 PF3 finding, got %d: %+v", tc.verify, len(pf3), out.Findings)
			}
			for _, want := range []string{"invalid Verify value(s)", tc.wantBad, "tests|build|lint|manual"} {
				if !strings.Contains(pf3[0].Message, want) {
					t.Errorf("PF3 message %q should contain %q", pf3[0].Message, want)
				}
			}
			if !strings.Contains(pf3[0].Fix, "**Verify:** tests | build | lint | manual") {
				t.Errorf("PF3 fix %q should write the accepted Verify shape", pf3[0].Fix)
			}
		})
	}
}

func TestValidatePlanFormatMultiIssueMessagesAreLists(t *testing.T) {
	root := t.TempDir()
	plan := strings.Replace(goodPlan, "**Complexity:** Standard", "**Complexity:** Bogus", 1)
	plan = strings.Replace(plan, "**Risk:** Low\n**Depends on:** none", "**Risk:** Extreme\n**Depends on:** none", 1)
	plan = strings.Replace(plan, "**Verify:** tests\n", "**Verify:** flaky\n", 1)
	writeFile(t, filepath.Join(root, "plan.md"), plan)

	out, err := validate(root, ValidateIn{Action: "plan_format", File: "plan.md"})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	pf3 := findingsByID(out.Findings, "PF3")
	if len(pf3) != 1 {
		t.Fatalf("expected 1 PF3 finding, got %d: %+v", len(pf3), out.Findings)
	}
	msg := pf3[0].Message
	if strings.Contains(msg, "; ") {
		t.Errorf("PF3 message must not join sub-issues with %q: %q", "; ", msg)
	}
	if got := strings.Count(msg, "\n- Task 1: "); got != 3 {
		t.Errorf("PF3 message has %d %q bullets, want 3 (Complexity, Risk, Verify): %q", got, "- Task 1: ", msg)
	}
}

func TestValidatePlanFormatFindingsCarryPathAndFix(t *testing.T) {
	root := t.TempDir()
	plan := strings.Replace(goodPlan, "## Deviations & assumptions\n\nNone.\n\n", "", 1)
	plan = strings.Replace(plan, "**Contract:**\n- shape: does X\n- names: Foo\n- mirror: existing pattern in bar.go\n- decisions: none\n- sync: none\n", "", 1)
	plan = strings.Replace(plan, "### Task 2:", "### Task 4:", 1)
	plan = strings.Replace(plan, "## Verification Scorecard\n\nAll good.\n", "", 1)
	writeFile(t, filepath.Join(root, "plan.md"), plan)

	out, err := validate(root, ValidateIn{Action: "plan_format", File: "plan.md", Final: true})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}

	wantPath := filepath.Join(root, "plan.md")
	markers := map[string][]string{
		"PF2": {"### Task N: Title", "**Complexity:**", "**Verify:**", "**Acceptance criteria:**"},
		"PF6": {"## Deviations & assumptions", "| Item | asked | does | why |"},
		"PF7": {"**Contract:**", "- shape", "- names", "- mirror", "- decisions", "- sync"},
		"PF9": {"## Verification Scorecard", "traceability matrix"},
	}
	for id, wantMarkers := range markers {
		found := findingsByID(out.Findings, id)
		if len(found) != 1 {
			t.Fatalf("expected 1 %s finding, got %d: %+v", id, len(found), out.Findings)
		}
		f := found[0]
		if f.Path != wantPath {
			t.Errorf("%s Path = %q, want %q", id, f.Path, wantPath)
		}
		for _, m := range wantMarkers {
			if !strings.Contains(f.Fix, m) {
				t.Errorf("%s fix should contain %q, got:\n%s", id, m, f.Fix)
			}
		}
	}

	// The failing task numbers travel in the message where the check knows them.
	if pf7 := findingsByID(out.Findings, "PF7"); len(pf7) == 1 && !strings.Contains(pf7[0].Message, "Task 1") {
		t.Errorf("PF7 message %q should name Task 1", pf7[0].Message)
	}
	if pf2 := findingsByID(out.Findings, "PF2"); len(pf2) == 1 && !strings.Contains(pf2[0].Message, "gap between Task 1 and Task 4") {
		t.Errorf("PF2 message %q should name the gap", pf2[0].Message)
	}
}

func TestValidatePlanFormatPF11MessageNamesConfigKey(t *testing.T) {
	tasks := []planTask{{Number: 2, Title: "Second", Body: "no owner field here\n"}}
	result := checkPF11(tasks, []string{"Owner"})
	if result.status != "fail" {
		t.Fatalf("status = %q, want fail", result.status)
	}
	for _, want := range []string{"plan.tasks.requiredFields", "Task 2", "'Owner'"} {
		if !strings.Contains(result.message, want) {
			t.Errorf("PF11 message %q should contain %q", result.message, want)
		}
	}
	if !strings.Contains(result.fix, "**Owner:** <value>") {
		t.Errorf("PF11 fix %q should show the missing field's shape", result.fix)
	}
}

// docPointerLineRe matches a line that only sends the reader elsewhere: a
// markdown file name, the words "reference" or "documentation", or an
// instruction to see/read/consult a doc.
var docPointerLineRe = regexp.MustCompile(`(?i)\.md\b|\b(reference|documentation)\b|\b(see|read|consult|refer to)\b.*\bdocs?\b`)

// fixPointsOnlyAtDoc reports whether every non-blank line of fix is a pointer
// at a document. An empty fix is not a pointer: it means the message already
// says everything.
func fixPointsOnlyAtDoc(fix string) bool {
	lines, pointers := 0, 0
	for _, line := range strings.Split(fix, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		lines++
		if docPointerLineRe.MatchString(line) {
			pointers++
		}
	}
	return lines > 0 && lines == pointers
}

func TestFixPointsOnlyAtDoc(t *testing.T) {
	pointers := []string{
		"see plan-format-reference.md",
		"see the reference documentation for the accepted shape",
		"refer to plan-format-reference.md\nread the docs",
	}
	for _, fix := range pointers {
		if !fixPointsOnlyAtDoc(fix) {
			t.Errorf("fixPointsOnlyAtDoc(%q) = false, want true", fix)
		}
	}
	notPointers := []string{
		"",
		"  ## Deviations & assumptions\n  | Item | asked | does | why |",
		"write the block below (see plan-format-reference.md):\n  **Contract:**\n  - shape: x",
		"  - shape (<code|docs|openspec>): the decided shape",
	}
	for _, fix := range notPointers {
		if fixPointsOnlyAtDoc(fix) {
			t.Errorf("fixPointsOnlyAtDoc(%q) = true, want false", fix)
		}
	}
}

// TestPlanFormatFixesAreSelfContained runs every plan-format check through each
// of its failure shapes and asserts the result stands on its own: a fix never
// consists of only a pointer at a reference document, sub-issues are not
// joined with "; ", the text is plain ASCII (no emoji), and PF2, PF6, PF7 and
// PF9 always write the accepted shape inline.
func TestPlanFormatFixesAreSelfContained(t *testing.T) {
	contractless := []planTask{{Number: 1, Title: "T", Body: "- Create: foo.go\n"}}
	shallowContract := []planTask{{Number: 1, Title: "T", Body: "- Create: foo.go\n**Contract:** does X\n"}}
	badMeta := "**Complexity:** Bogus\n**Risk:** Low\n**Depends on:** none\n**Verify:** tests\n"

	cases := []struct {
		name        string
		check       pfCheck
		mustHaveFix bool
	}{
		{"PF1 missing header fields", checkPF1("no header fields here"), false},
		{"PF2 no tasks", checkPF2(nil), true},
		{"PF2 numbering starts high", checkPF2([]planTask{{Number: 5}}), true},
		{"PF2 gap", checkPF2([]planTask{{Number: 1}, {Number: 3}}), true},
		{"PF2 duplicate", checkPF2([]planTask{{Number: 1}, {Number: 1}}), true},
		{"PF3 invalid complexity", checkPF3([]planTask{{Number: 1, Body: badMeta}}), false},
		{"PF3 missing verify and depends", checkPF3([]planTask{{Number: 1, Body: "**Complexity:** Trivial\n**Risk:** Low\n"}}), false},
		{"PF3 invalid verify", checkPF3([]planTask{{Number: 1, Body: "**Complexity:** Trivial\n**Risk:** Low\n**Depends on:** none\n**Verify:** flaky\n"}}), false},
		{"PF4 nonexistent dependency", checkPF4([]planTask{{Number: 1, Body: "**Depends on:** Task 9\n"}}), false},
		{"PF4 cycle", checkPF4([]planTask{{Number: 1, Body: "**Depends on:** Task 2\n"}, {Number: 2, Body: "**Depends on:** Task 1\n"}}), false},
		{"PF5 missing acceptance criteria", checkPF5([]planTask{{Number: 1, Body: "nothing here\n"}}), false},
		{"PF5 no checkbox", checkPF5([]planTask{{Number: 1, Body: "**Acceptance criteria:**\nplain text\n"}}), false},
		{"PF6 missing deviations", checkPF6("no such section"), true},
		{"PF7 missing contract", checkPF7(contractless), true},
		{"PF9 missing scorecard", checkPF9("no such section"), true},
		{"PF10 missing template section", checkPF10("no such section", []string{"Rollback Plan"}, "plan-template.md"), false},
		{"PF11 missing custom field", checkPF11([]planTask{{Number: 1, Body: "x\n"}}, []string{"Owner"}), false},
		{"PF12 no contract block", checkPF12(contractless, "full"), false},
		{"PF12 shallow contract", checkPF12(shallowContract, "full"), false},
	}

	seen := map[string]bool{}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := tc.check
			seen[c.id] = true
			if c.status != "fail" {
				t.Fatalf("status = %q, want fail (the fixture must trigger the failure)", c.status)
			}
			if c.message == "" {
				t.Error("failure message is empty")
			}
			if tc.mustHaveFix && c.fix == "" {
				t.Errorf("%s must carry a fix that writes the accepted shape inline", c.id)
			}
			if fixPointsOnlyAtDoc(c.fix) {
				t.Errorf("%s fix only points at a document:\n%s", c.id, c.fix)
			}
			if strings.Contains(c.message, "; ") {
				t.Errorf("%s message joins sub-issues with %q: %q", c.id, "; ", c.message)
			}
			for _, r := range c.message + c.fix {
				if r > 127 {
					t.Errorf("%s text contains non-ASCII rune %q", c.id, r)
					break
				}
			}
		})
	}

	// Every check that can fail is covered. PF8 does not exist in this format.
	for _, id := range []string{"PF1", "PF2", "PF3", "PF4", "PF5", "PF6", "PF7", "PF9", "PF10", "PF11", "PF12"} {
		if !seen[id] {
			t.Errorf("no failure case for %s in TestPlanFormatFixesAreSelfContained", id)
		}
	}
}

func TestFindingFixJSONOmitEmpty(t *testing.T) {
	without, err := json.Marshal(discovery.Finding{ID: "PF1", Severity: "error", Message: "m"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(without), "fix") {
		t.Errorf("Finding without Fix must not emit a fix key: %s", without)
	}

	with, err := json.Marshal(discovery.Finding{ID: "PF1", Severity: "error", Message: "m", Fix: "do x"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(with), `"fix":"do x"`) {
		t.Errorf("Finding with Fix must emit the fix key: %s", with)
	}
}

// ---------------------------------------------------------------------------
// ValidatePlanFormatForHook (post-tool-validate hook entry point)
// ---------------------------------------------------------------------------

// planWithoutScorecard passes every per-edit check; only the final-only PF9
// scorecard is missing.
func planWithoutScorecard() string {
	return strings.Replace(goodPlan, "## Verification Scorecard\n\nAll good.\n", "", 1)
}

// planBlockedByPF6 fails PF6 (per-edit) and also lacks the PF9 scorecard.
func planBlockedByPF6() string {
	return strings.Replace(planWithoutScorecard(), "## Deviations & assumptions\n\nNone.\n\n", "", 1)
}

const templateWithRollback = `# Plan Template

## Required Sections

- Rollback Plan
`

// hookRoot returns a fresh project root holding plan.md, with no plugin root
// set, so a test only sees the templates it writes itself.
func hookRoot(t *testing.T, plan string) string {
	t.Helper()
	t.Setenv("CLAUDE_PLUGIN_ROOT", "")
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "plan.md"), plan)
	return root
}

func TestValidatePlanFormatForHook_NothingBlocking_NoOutput(t *testing.T) {
	root := hookRoot(t, planWithoutScorecard())
	// Both final-only checks would fail here (no scorecard, template section
	// missing). With nothing blocking they must not be reported.
	writeFile(t, filepath.Join(root, paths.DataDir, "plan-template.md"), templateWithRollback)

	blocking, final, err := ValidatePlanFormatForHook(root, "plan.md")
	if err != nil {
		t.Fatalf("ValidatePlanFormatForHook: %v", err)
	}
	if blocking != nil || final != nil {
		t.Errorf("want nil, nil for a plan with nothing blocking; got blocking=%+v final=%+v", blocking, final)
	}
}

func TestValidatePlanFormatForHook_Blocking_NoTemplate_ReportsPF9Only(t *testing.T) {
	root := hookRoot(t, planBlockedByPF6())

	blocking, final, err := ValidatePlanFormatForHook(root, "plan.md")
	if err != nil {
		t.Fatalf("ValidatePlanFormatForHook: %v", err)
	}
	if len(findingsByID(blocking, "PF6")) != 1 {
		t.Fatalf("want a PF6 blocking finding, got %+v", blocking)
	}
	if len(final) != 1 || final[0].ID != "PF9" {
		t.Fatalf("want exactly PF9 in the final-only list when no template resolves, got %+v", final)
	}
	if final[0].Path != filepath.Join(root, "plan.md") || !strings.Contains(final[0].Fix, "## Verification Scorecard") {
		t.Errorf("PF9 should carry Path and an inline fix, got %+v", final[0])
	}
	for _, f := range blocking {
		if f.ID == "PF9" || f.ID == "PF10" {
			t.Errorf("%s must not appear in the blocking list", f.ID)
		}
	}
}

func TestValidatePlanFormatForHook_ProjectTemplate_AddsPF10(t *testing.T) {
	root := hookRoot(t, planBlockedByPF6())
	tpl := filepath.Join(root, paths.DataDir, "plan-template.md")
	writeFile(t, tpl, templateWithRollback)

	_, final, err := ValidatePlanFormatForHook(root, "plan.md")
	if err != nil {
		t.Fatalf("ValidatePlanFormatForHook: %v", err)
	}
	pf10 := findingsByID(final, "PF10")
	if len(pf10) != 1 {
		t.Fatalf("want 1 PF10 in the final-only list, got %+v", final)
	}
	if !strings.Contains(pf10[0].Message, "Rollback Plan") || !strings.Contains(pf10[0].Message, tpl) {
		t.Errorf("PF10 message %q should name the missing section and the template path", pf10[0].Message)
	}
	if len(findingsByID(final, "PF9")) != 1 {
		t.Errorf("PF9 should still be reported next to PF10, got %+v", final)
	}
}

func TestValidatePlanFormatForHook_PluginDefaultTemplate(t *testing.T) {
	root := hookRoot(t, planBlockedByPF6())
	pluginRoot := t.TempDir()
	t.Setenv("CLAUDE_PLUGIN_ROOT", pluginRoot)
	defaultTpl := filepath.Join(pluginRoot, "skills", "plan", "plan-template-default.md")
	writeFile(t, defaultTpl, templateWithRollback)

	_, final, err := ValidatePlanFormatForHook(root, "plan.md")
	if err != nil {
		t.Fatalf("ValidatePlanFormatForHook: %v", err)
	}
	pf10 := findingsByID(final, "PF10")
	if len(pf10) != 1 || !strings.Contains(pf10[0].Message, defaultTpl) {
		t.Fatalf("want PF10 naming the CLAUDE_PLUGIN_ROOT default template %s, got %+v", defaultTpl, final)
	}
}

func TestValidatePlanFormatForHook_ProjectTemplateWinsOverPluginDefault(t *testing.T) {
	root := hookRoot(t, planBlockedByPF6())
	projectTpl := filepath.Join(root, paths.DataDir, "plan-template.md")
	writeFile(t, projectTpl, templateWithRollback)
	pluginRoot := t.TempDir()
	t.Setenv("CLAUDE_PLUGIN_ROOT", pluginRoot)
	writeFile(t, filepath.Join(pluginRoot, "skills", "plan", "plan-template-default.md"), templateWithRollback)

	_, final, err := ValidatePlanFormatForHook(root, "plan.md")
	if err != nil {
		t.Fatalf("ValidatePlanFormatForHook: %v", err)
	}
	pf10 := findingsByID(final, "PF10")
	if len(pf10) != 1 || !strings.Contains(pf10[0].Message, projectTpl) {
		t.Fatalf("want a single PF10 naming the project template %s, got %+v", projectTpl, final)
	}
}

func TestValidatePlanFormatForHook_UnreadableTemplate_SkipsPF10(t *testing.T) {
	root := hookRoot(t, planBlockedByPF6())
	// A directory where the template file should be: reading it fails.
	if err := os.MkdirAll(filepath.Join(root, paths.DataDir, "plan-template.md"), 0o755); err != nil {
		t.Fatal(err)
	}

	blocking, final, err := ValidatePlanFormatForHook(root, "plan.md")
	if err != nil {
		t.Fatalf("a template that cannot be read must not fail the hook, got: %v", err)
	}
	if len(blocking) == 0 {
		t.Error("blocking findings must still be returned")
	}
	if len(final) != 1 || final[0].ID != "PF9" {
		t.Errorf("want PF9 only when the template cannot be read, got %+v", final)
	}
}

func TestValidatePlanFormatForHook_MissingPlanFile_ErrorHasSuggestion(t *testing.T) {
	root := hookRoot(t, goodPlan)

	_, _, err := ValidatePlanFormatForHook(root, "missing.md")
	var domErr *mcpserver.DomainError
	if !errors.As(err, &domErr) {
		t.Fatalf("want *mcpserver.DomainError, got %T: %v", err, err)
	}
	if domErr.Suggestion == "" {
		t.Error("DomainError must carry a Suggestion")
	}
}

func TestHookPlanTemplateCandidates(t *testing.T) {
	root := t.TempDir()

	t.Setenv("CLAUDE_PLUGIN_ROOT", "")
	got := hookPlanTemplateCandidates(root)
	if len(got) != 1 || got[0] != filepath.Join(root, paths.DataDir, "plan-template.md") {
		t.Errorf("without CLAUDE_PLUGIN_ROOT want only the project template, got %q", got)
	}

	t.Setenv("CLAUDE_PLUGIN_ROOT", "/plugin")
	got = hookPlanTemplateCandidates(root)
	want := []string{
		filepath.Join(root, paths.DataDir, "plan-template.md"),
		filepath.Join("/plugin", "skills", "plan", "plan-template-default.md"),
	}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("candidates = %q, want %q (project override first)", got, want)
	}
}
