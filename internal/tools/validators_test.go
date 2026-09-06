package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rnagrodzki/sdlc-plugin/internal/discovery"
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
**Contract:** does X

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
	plan := strings.Replace(goodPlan, "**Contract:** does X\n", "", 1)
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
	writeFile(t, filepath.Join(root, ".sdlc", "pr-template.md"), tmpl)

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
	writeFile(t, filepath.Join(root, ".sdlc", "pr-template.md"), "   \n\n  ")
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
	writeFile(t, filepath.Join(root, ".sdlc", "pr-template.md"), "just some text, no headings at all")
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
	writeFile(t, filepath.Join(root, ".sdlc", "pr-template.md"), tmpl)
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
	writeFile(t, filepath.Join(root, ".sdlc", "pr-template.md"), tmpl)
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

func writeConfigSection(t *testing.T, root, section string, data any) {
	t.Helper()
	raw := map[string]any{section: data}
	b, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	writeFile(t, filepath.Join(root, ".sdlc", "config.json"), string(b))
}

func TestValidateGuardrailsAllChecks(t *testing.T) {
	root := t.TempDir()
	writeConfigSection(t, root, "plan", map[string]any{
		"guardrails": []map[string]any{
			{"id": "good-guardrail", "description": "A valid guardrail description."},
			{"id": "Bad_ID", "description": "desc"},
			{"id": "dup-id", "description": "d1"},
			{"id": "dup-id", "description": "d2"},
			{"id": "sev-bad", "description": "d3", "severity": "critical"},
			{"description": "missing id"},
			{"id": "no-desc"},
		},
	})

	findingsOut, err := validate(root, ValidateIn{Action: "guardrails"})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	findings := findingsOut.Findings
	if len(findings) != 5 {
		t.Fatalf("expected 5 guardrail findings, got %d: %+v", len(findings), findings)
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
	if byID["dup-id"] != 1 {
		t.Errorf("expected 1 finding for dup-id (the duplicate), got %d", byID["dup-id"])
	}
	if byID["sev-bad"] != 1 {
		t.Errorf("expected 1 finding for sev-bad, got %d", byID["sev-bad"])
	}
	if byID["(missing)"] != 1 {
		t.Errorf("expected 1 finding for (missing) id, got %d", byID["(missing)"])
	}
	if byID["no-desc"] != 1 {
		t.Errorf("expected 1 finding for no-desc, got %d", byID["no-desc"])
	}
}

func TestValidateGuardrailsNoSectionIsPass(t *testing.T) {
	root := t.TempDir() // no .sdlc/config.json at all
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
	writeConfigSection(t, root, "plan", map[string]any{})
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
	writeConfigSection(t, root, "execute", map[string]any{
		"guardrails": []map[string]any{
			{"id": "exec-guardrail", "description": ""},
		},
	})
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
	writeFile(t, filepath.Join(root, ".sdlc", "review-dimensions", "a-dim.md"), "---\n"+
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
	writeFile(t, filepath.Join(root, ".sdlc", "review-dimensions", "bad-dim.md"), "---\n"+
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
	writeFile(t, filepath.Join(root, ".sdlc", "review-dimensions", "a-dim.md"), dim)
	writeFile(t, filepath.Join(root, ".sdlc", "review-dimensions", "b-dim.md"), dim)

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

	logPath := filepath.Join(root, ".sdlc", "learnings", "log.md")
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
