package tools

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/rnagrodzki/sdlc-plugin/internal/commstyle"
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

// TestValidatePlanFormatPF1EmptyFieldBeforeLabel verifies an empty
// "**Goal:**" does not take the next "**Architecture:** ..." line as its
// value, while a plain value on the next line still counts.
func TestValidatePlanFormatPF1EmptyFieldBeforeLabel(t *testing.T) {
	cases := []struct {
		name    string
		goal    string
		wantPF1 bool
	}{
		{"next line is a label", "**Goal:**\n", true},
		{"next line is a label after blank lines", "**Goal:**\n\n", true},
		{"next line is a plain value", "**Goal:**\nDo the thing\n", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			plan := strings.Replace(goodPlan, "**Goal:** Do the thing\n", tc.goal, 1)
			if plan == goodPlan {
				t.Fatal("fixture replace did not change goodPlan")
			}
			writeFile(t, filepath.Join(root, "plan.md"), plan)

			findingsOut, err := validate(root, ValidateIn{Action: "plan_format", File: "plan.md"})
			if err != nil {
				t.Fatalf("validate: %v", err)
			}
			pf1 := findingsByID(findingsOut.Findings, "PF1")
			if !tc.wantPF1 {
				if len(pf1) != 0 {
					t.Fatalf("expected no PF1 finding, got %+v", pf1)
				}
				return
			}
			if len(pf1) != 1 {
				t.Fatalf("expected 1 PF1 finding, got %d: %+v", len(pf1), findingsOut.Findings)
			}
			if !strings.Contains(pf1[0].Message, "Goal") {
				t.Errorf("PF1 message %q should mention Goal", pf1[0].Message)
			}
			if strings.Contains(pf1[0].Message, "Architecture") {
				t.Errorf("PF1 message %q should not mention Architecture: it has a value", pf1[0].Message)
			}
		})
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

func TestValidatePlanFormatPF3EmptyFieldBeforeLabel(t *testing.T) {
	root := t.TempDir()
	plan := strings.Replace(goodPlan, "**Complexity:** Standard\n", "**Complexity:**\n", 1)
	if plan == goodPlan {
		t.Fatal("fixture replace did not change goodPlan")
	}
	writeFile(t, filepath.Join(root, "plan.md"), plan)

	findingsOut, err := validate(root, ValidateIn{Action: "plan_format", File: "plan.md"})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	pf3 := findingsByID(findingsOut.Findings, "PF3")
	if len(pf3) != 1 {
		t.Fatalf("expected 1 PF3 finding, got %d: %+v", len(pf3), findingsOut.Findings)
	}
	if !strings.Contains(pf3[0].Message, "Complexity") {
		t.Errorf("PF3 message %q should name the empty Complexity field", pf3[0].Message)
	}
	if strings.Contains(pf3[0].Message, "Risk") {
		t.Errorf("PF3 message %q should not mention Risk: it has a value", pf3[0].Message)
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

// TestValidatePlanFormatFile_UnreadableIsNotFileNotFound pins that a plan
// path which exists but cannot be read (here: a directory, which
// os.ReadFile refuses) is reported as unreadable, not as "file not found".
// The two need different fixes: re-checking an already-correct path versus
// fixing permissions or the file type.
func TestValidatePlanFormatFile_UnreadableIsNotFileNotFound(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "plan.md"), 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := validate(root, ValidateIn{Action: "plan_format", File: "plan.md"})
	var domErr *mcpserver.DomainError
	if !errors.As(err, &domErr) {
		t.Fatalf("want *mcpserver.DomainError, got %T: %v", err, err)
	}
	if strings.Contains(domErr.Msg, "file not found") {
		t.Errorf("Msg = %q, an existing-but-unreadable path must not say file not found", domErr.Msg)
	}
	if !strings.Contains(domErr.Msg, "cannot read") {
		t.Errorf("Msg = %q, want it to say cannot read", domErr.Msg)
	}
	if !strings.Contains(domErr.Suggestion, "regular file") {
		t.Errorf("Suggestion = %q, want it to mention that the path must be a regular file", domErr.Suggestion)
	}
}

// TestValidatePlanFormatFinal_UnreadableTemplateErrors pins the same split
// for the PF10 template path when validatePlanFormat reads it directly
// (final mode, not through ValidatePlanFormatForHook's skip-on-unreadable
// loop): an existing-but-unreadable template errors as unreadable, not as
// "template not found".
func TestValidatePlanFormatFinal_UnreadableTemplateErrors(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "plan.md"), goodPlan)
	if err := os.MkdirAll(filepath.Join(root, "template.md"), 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := validate(root, ValidateIn{Action: "plan_format", File: "plan.md", Final: true, Template: "template.md"})
	var domErr *mcpserver.DomainError
	if !errors.As(err, &domErr) {
		t.Fatalf("want *mcpserver.DomainError, got %T: %v", err, err)
	}
	if strings.Contains(domErr.Msg, "template not found") {
		t.Errorf("Msg = %q, an existing-but-unreadable template must not say template not found", domErr.Msg)
	}
	if !strings.Contains(domErr.Msg, "cannot read") {
		t.Errorf("Msg = %q, want it to say cannot read", domErr.Msg)
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

// TestValidateCostTiersPluginLayout pins the scan to this repo's real plugin
// layout (plugins/sdlc/skills, plugins/sdlc/agents), not the flat fallback.
func TestValidateCostTiersPluginLayout(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "plugins", "sdlc", "skills", "plan", "SKILL.md"),
		"---\nname: plan\ndescription: a skill\nmodel: haiku\n---\nBody.\n")
	writeFile(t, filepath.Join(root, "plugins", "sdlc", "agents", "helper.md"),
		"---\nname: helper\ndescription: an agent\nmodel: sonnet\n---\nBody.\n")
	writeCostTiersDoc(t, root,
		[][2]string{{"plan", "opus"}},
		[][2]string{{"helper", "sonnet"}},
	)

	out, err := validate(root, ValidateIn{Action: "cost_tiers"})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	drift := findingsByID(out.Findings, "DRIFT")
	if len(drift) != 1 || !strings.Contains(drift[0].Message, "plan") {
		t.Errorf("expected 1 DRIFT finding for skill plan, got %+v", out.Findings)
	}
	if stale := findingsByID(out.Findings, "STALE_DOC"); len(stale) != 0 {
		t.Errorf("expected no STALE_DOC findings (skill and agent were found), got %+v", stale)
	}
}

// TestValidateCostTiersDocMissing: a project without docs/cost-tiers.md gets
// one warning saying the check was skipped, not an error.
func TestValidateCostTiersDocMissing(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "some-skill", "opus")
	out, err := validate(root, ValidateIn{Action: "cost_tiers"})
	if err != nil {
		t.Fatalf("missing docs/cost-tiers.md must not be an error, got %v", err)
	}
	if len(out.Findings) != 1 {
		t.Fatalf("expected exactly 1 finding, got %+v", out.Findings)
	}
	f := out.Findings[0]
	if f.ID != "NO_COST_DOC" || f.Severity != "warning" || f.Path != filepath.Join("docs", "cost-tiers.md") {
		t.Errorf("unexpected finding: %+v", f)
	}
	if !strings.Contains(f.Message, "skipped") {
		t.Errorf("message should say the check was skipped, got %q", f.Message)
	}
}

// ---------------------------------------------------------------------------
// guardrails
// ---------------------------------------------------------------------------

// TestValidateGuardrailsAllChecks exercises validateOneGuardrail's checks via
// a real config.toml in the canonical named-table form
// ([plan.guardrails.<id>]), where config.ReadSection injects "id" from the
// table key. The "id is missing" and "id is duplicated" checks are covered
// by TestValidateGuardrailsIDChecksReachable, which uses the config shapes
// that skip that injection.
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

// TestValidateGuardrailsIDChecksReachable proves the "id is missing" and
// "id is duplicated across guardrails" checks are reachable from a real
// config.toml. normalizeGuardrailTables only rewrites the named-table form
// (a map); an array form ([[plan.guardrails]] or an inline array) reaches
// the validator as-is, with no id injected and no key uniqueness. A quoted
// empty table key ([plan.guardrails.""]) is valid TOML and injects id "".
func TestValidateGuardrailsIDChecksReachable(t *testing.T) {
	cases := []struct {
		name   string
		config string
		want   []string // expected finding messages, in order
	}{
		{
			name: "array of tables: missing id and duplicate id",
			config: "" +
				"[[plan.guardrails]]\n" +
				"description = \"no id here\"\n" +
				"\n" +
				"[[plan.guardrails]]\n" +
				"id = \"dup-id\"\n" +
				"description = \"first\"\n" +
				"\n" +
				"[[plan.guardrails]]\n" +
				"id = \"dup-id\"\n" +
				"description = \"second\"\n",
			want: []string{
				"(missing): id is missing",
				"dup-id: id is duplicated across guardrails",
			},
		},
		{
			name:   "inline array: missing id",
			config: "[plan]\nguardrails = [ { description = \"x\" } ]\n",
			want:   []string{"(missing): id is missing"},
		},
		{
			name:   "empty quoted table key: missing id",
			config: "[plan.guardrails.\"\"]\ndescription = \"d\"\n",
			want:   []string{"(missing): id is missing"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeFile(t, filepath.Join(root, paths.DataDir, "config.toml"), tc.config)
			out, err := validate(root, ValidateIn{Action: "guardrails"})
			if err != nil {
				t.Fatalf("validate: %v", err)
			}
			var got []string
			for _, f := range out.Findings {
				if f.Severity != "error" {
					t.Errorf("finding severity = %q, want error: %+v", f.Severity, f)
				}
				got = append(got, f.Message)
			}
			if strings.Join(got, "\n") != strings.Join(tc.want, "\n") {
				t.Fatalf("findings = %q, want %q", got, tc.want)
			}
		})
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

// TestValidateGuardrailsActiveWorktreeFlag pins ValidateIn.ActiveWorktree's
// two contract points: it does not change what validateGuardrailsAction
// computes once a root has been chosen (the main-vs-active swap itself is
// validateRoot's job, covered in validators_worktree_test.go), and every
// action other than guardrails ignores it outright.
func TestValidateGuardrailsActiveWorktreeFlag(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, paths.DataDir, "config.toml"), ""+
		"[plan.guardrails.good-guardrail]\n"+
		"description = \"A valid guardrail description.\"\n"+
		"\n"+
		"[plan.guardrails.Bad_ID]\n"+
		"description = \"desc\"\n")

	withFlag, err := validate(root, ValidateIn{Action: "guardrails", ActiveWorktree: true})
	if err != nil {
		t.Fatalf("validate(guardrails, activeWorktree=true): %v", err)
	}
	withoutFlag, err := validate(root, ValidateIn{Action: "guardrails"})
	if err != nil {
		t.Fatalf("validate(guardrails): %v", err)
	}
	if len(withFlag.Findings) != len(withoutFlag.Findings) || len(withFlag.Findings) != 1 {
		t.Fatalf("activeWorktree flag changed guardrails findings: with=%+v without=%+v", withFlag.Findings, withoutFlag.Findings)
	}

	for _, action := range []string{"discovery", "dimensions"} {
		ignoring, err := validate(root, ValidateIn{Action: action})
		if err != nil {
			t.Fatalf("validate(%s): %v", action, err)
		}
		respecting, err := validate(root, ValidateIn{Action: action, ActiveWorktree: true})
		if err != nil {
			t.Fatalf("validate(%s, activeWorktree=true): %v", action, err)
		}
		if len(ignoring.Findings) != len(respecting.Findings) {
			t.Errorf("%s: activeWorktree flag changed findings, want it ignored: without=%+v with=%+v", action, ignoring.Findings, respecting.Findings)
		}
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

func TestValidateGuardrailsMalformedConfig_InfraErrorWithAndWithoutCandidates(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, paths.DataDir, "config.toml"), "[[[not toml\n")

	for _, candidatesJSON := range []string{"", `[{"id":"x","description":"d","severity":"error"}]`} {
		_, err := validate(root, ValidateIn{Action: "guardrails", CandidatesJSON: candidatesJSON})
		var infra *mcpserver.InfraError
		if !errors.As(err, &infra) {
			t.Fatalf("candidatesJson=%q: want *mcpserver.InfraError, got %T: %v", candidatesJSON, err, err)
		}
		if !strings.HasPrefix(infra.Msg, "read plan guardrails section:") {
			t.Errorf("candidatesJson=%q: InfraError.Msg = %q, want prefix %q", candidatesJSON, infra.Msg, "read plan guardrails section:")
		}
		if infra.Suggestion == "" {
			t.Errorf("candidatesJson=%q: InfraError must carry a Suggestion", candidatesJSON)
		}
	}
}

// ---------------------------------------------------------------------------
// guardrails: description byte-count wording (description exceeds 1024
// bytes, not "characters") and the finding fix hints.
// ---------------------------------------------------------------------------

func TestValidateGuardrailsDescriptionByteCountMessage(t *testing.T) {
	over := strings.Repeat("a", 1310)
	atLimit := strings.Repeat("a", 1024)
	root := t.TempDir()
	writeFile(t, filepath.Join(root, paths.DataDir, "config.toml"), ""+
		"[plan.guardrails.dry]\n"+
		"description = \""+over+"\"\n"+
		"\n"+
		"[plan.guardrails.at-limit]\n"+
		"description = \""+atLimit+"\"\n")

	findingsOut, err := validate(root, ValidateIn{Action: "guardrails"})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	dry := findingsByID(findingsOut.Findings, "dry")
	if len(dry) != 1 {
		t.Fatalf("expected exactly 1 finding for dry, got %+v", dry)
	}
	wantMsg := "dry: description exceeds 1024 bytes (1310 bytes, 286 over)"
	if dry[0].Message != wantMsg {
		t.Errorf("message = %q, want %q", dry[0].Message, wantMsg)
	}
	wantFix := "Shorten the description to 1024 bytes or less, or split it into independent guardrails with ids dry-1, dry-2, each a complete rule."
	if dry[0].Fix != wantFix {
		t.Errorf("fix = %q, want %q", dry[0].Fix, wantFix)
	}
	if at := findingsByID(findingsOut.Findings, "at-limit"); len(at) != 0 {
		t.Errorf("description at exactly 1024 bytes must not trigger a length finding, got %+v", at)
	}
}

func TestValidateGuardrailsFindingFixHints(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, paths.DataDir, "config.toml"), ""+
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
	for _, f := range findingsOut.Findings {
		if f.Fix == "" {
			t.Errorf("finding %+v must carry a non-empty fix", f)
		}
	}

	idFinding := findingsByID(findingsOut.Findings, "Bad_ID")
	if len(idFinding) != 1 {
		t.Fatalf("expected 1 finding for Bad_ID, got %+v", idFinding)
	}
	if want := "Rename the id to lowercase words joined by single hyphens, e.g. no-ci-bypass."; idFinding[0].Fix != want {
		t.Errorf("Bad_ID fix = %q, want %q", idFinding[0].Fix, want)
	}

	sevFinding := findingsByID(findingsOut.Findings, "sev-bad")
	if len(sevFinding) != 1 {
		t.Fatalf("expected 1 finding for sev-bad, got %+v", sevFinding)
	}
	if want := "Set severity to error or warning."; sevFinding[0].Fix != want {
		t.Errorf("sev-bad fix = %q, want %q", sevFinding[0].Fix, want)
	}
}

func TestValidateGuardrailsDuplicateIDFixHint(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, paths.DataDir, "config.toml"), ""+
		"[[plan.guardrails]]\n"+
		"id = \"dup-id\"\n"+
		"description = \"first\"\n"+
		"\n"+
		"[[plan.guardrails]]\n"+
		"id = \"dup-id\"\n"+
		"description = \"second\"\n")

	findingsOut, err := validate(root, ValidateIn{Action: "guardrails"})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if len(findingsOut.Findings) != 1 {
		t.Fatalf("expected 1 finding (duplicate id), got %+v", findingsOut.Findings)
	}
	want := "Use action consolidate on the current id, or pick a new unique id."
	if findingsOut.Findings[0].Fix != want {
		t.Errorf("fix = %q, want %q", findingsOut.Findings[0].Fix, want)
	}
}

// ---------------------------------------------------------------------------
// guardrails: candidatesJson in-memory check (no write to disk)
// ---------------------------------------------------------------------------

func TestValidateGuardrailsCandidates_ReplacesDiskEntry(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, paths.DataDir, "config.toml")
	over := strings.Repeat("a", 1310)
	original := "[plan.guardrails.dry]\ndescription = \"" + over + "\"\n"
	writeFile(t, configPath, original)

	candidatesJSON := `[{"id":"dry","description":"Reuse existing helpers.","severity":"error"}]`
	findingsOut, err := validate(root, ValidateIn{Action: "guardrails", CandidatesJSON: candidatesJSON})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if len(findingsOut.Findings) != 0 {
		t.Fatalf("expected no findings (candidate replaces over-length disk entry), got %+v", findingsOut.Findings)
	}

	onDisk, rerr := os.ReadFile(configPath)
	if rerr != nil {
		t.Fatalf("read config.toml: %v", rerr)
	}
	if string(onDisk) != original {
		t.Fatalf("config.toml was modified: got %q, want unchanged %q", string(onDisk), original)
	}
}

func TestValidateGuardrailsCandidates_NewCandidateAdded(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, paths.DataDir, "config.toml"), ""+
		"[plan.guardrails.good-guardrail]\n"+
		"description = \"A valid guardrail description.\"\n")

	candidatesJSON := `[{"id":"Bad_ID","description":"x","severity":"error"}]`
	findingsOut, err := validate(root, ValidateIn{Action: "guardrails", CandidatesJSON: candidatesJSON})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if len(findingsOut.Findings) != 1 {
		t.Fatalf("expected 1 finding, got %+v", findingsOut.Findings)
	}
	if findingsOut.Findings[0].ID != "Bad_ID" {
		t.Errorf("finding ID = %q, want Bad_ID", findingsOut.Findings[0].ID)
	}
}

func TestValidateGuardrailsCandidates_NoConfigFile(t *testing.T) {
	root := t.TempDir() // no .sdlc-v2/config.toml at all

	candidatesJSON := `[{"id":"needs-review","description":"A valid description.","severity":"critical"}]`
	findingsOut, err := validate(root, ValidateIn{Action: "guardrails", CandidatesJSON: candidatesJSON})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if len(findingsOut.Findings) != 1 {
		t.Fatalf("expected 1 finding (invalid severity), got %+v", findingsOut.Findings)
	}
	if findingsOut.Findings[0].ID != "needs-review" {
		t.Errorf("finding ID = %q, want needs-review", findingsOut.Findings[0].ID)
	}
}

func TestValidateGuardrailsCandidates_MalformedJSON(t *testing.T) {
	root := t.TempDir()
	_, err := validate(root, ValidateIn{Action: "guardrails", CandidatesJSON: `[{"id":`})
	var domErr *mcpserver.DomainError
	if !errors.As(err, &domErr) {
		t.Fatalf("want *mcpserver.DomainError, got %T: %v", err, err)
	}
	if domErr.Suggestion == "" {
		t.Error("DomainError must carry a Suggestion")
	}
}

func TestValidateGuardrailsCandidates_NotArrayOfObjects(t *testing.T) {
	root := t.TempDir()
	for _, bad := range []string{`"just a string"`, `[1,2,3]`, `{"id":"x"}`} {
		_, err := validate(root, ValidateIn{Action: "guardrails", CandidatesJSON: bad})
		var domErr *mcpserver.DomainError
		if !errors.As(err, &domErr) {
			t.Fatalf("candidatesJson=%q: want *mcpserver.DomainError, got %T: %v", bad, err, err)
		}
	}
}

func TestValidateGuardrailsCandidates_WithoutCandidatesJSON_UnaffectedByFlag(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, paths.DataDir, "config.toml"), ""+
		"[plan.guardrails.good-guardrail]\n"+
		"description = \"A valid guardrail description.\"\n")

	withEmpty, err := validate(root, ValidateIn{Action: "guardrails", CandidatesJSON: ""})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	without, err := validate(root, ValidateIn{Action: "guardrails"})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if len(withEmpty.Findings) != 0 || len(without.Findings) != 0 {
		t.Fatalf("expected no findings in either case, got with=%+v without=%+v", withEmpty.Findings, without.Findings)
	}
}

// ---------------------------------------------------------------------------
// guardrails: severity downgrade check (harden is strengthen-only)
// ---------------------------------------------------------------------------

// guardrailDowngradeFix is the Fix text that the severity downgrade finding
// carries.
const guardrailDowngradeFix = "Keep severity error, or propose a new guardrail id for the weaker rule."

// TestValidateGuardrailsCandidates_SeverityDowngrade checks that a candidate
// which lowers a disk guardrail from error to warning gets one error finding,
// and that no other severity pair gets a finding.
func TestValidateGuardrailsCandidates_SeverityDowngrade(t *testing.T) {
	cases := []struct {
		name          string
		disk          string // [plan.guardrails.dry] body
		candidateSev  string // candidate severity JSON fragment, empty = omit
		wantDowngrade bool
	}{
		{"error to warning", `severity = "error"`, `,"severity":"warning"`, true},
		{"missing disk severity counts as error", ``, `,"severity":"warning"`, true},
		{"warning to error", `severity = "warning"`, `,"severity":"error"`, false},
		{"error to error", `severity = "error"`, `,"severity":"error"`, false},
		{"warning to warning", `severity = "warning"`, `,"severity":"warning"`, false},
		{"missing disk to error", ``, `,"severity":"error"`, false},
		{"error to missing candidate severity", `severity = "error"`, ``, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeFile(t, filepath.Join(root, paths.DataDir, "config.toml"),
				"[plan.guardrails.dry]\ndescription = \"Reuse existing helpers.\"\n"+tc.disk+"\n")

			candidatesJSON := `[{"id":"dry","description":"Reuse existing helpers."` + tc.candidateSev + `}]`
			out, err := validate(root, ValidateIn{Action: "guardrails", CandidatesJSON: candidatesJSON})
			if err != nil {
				t.Fatalf("validate: %v", err)
			}
			if !tc.wantDowngrade {
				if len(out.Findings) != 0 {
					t.Fatalf("expected no findings, got %+v", out.Findings)
				}
				return
			}
			if len(out.Findings) != 1 {
				t.Fatalf("expected exactly 1 finding, got %+v", out.Findings)
			}
			f := out.Findings[0]
			if f.ID != "dry" || f.Severity != "error" {
				t.Errorf("finding id/severity = %q/%q, want dry/error", f.ID, f.Severity)
			}
			wantMsg := "dry: severity lowered from error to warning (harden is strengthen-only)"
			if f.Message != wantMsg {
				t.Errorf("message = %q, want %q", f.Message, wantMsg)
			}
			if f.Fix != guardrailDowngradeFix {
				t.Errorf("fix = %q, want %q", f.Fix, guardrailDowngradeFix)
			}
		})
	}
}

// TestValidateGuardrailsCandidates_SeverityDowngrade_NewIDNoFinding checks that
// a warning candidate with an id not on disk gets no downgrade finding.
func TestValidateGuardrailsCandidates_SeverityDowngrade_NewIDNoFinding(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, paths.DataDir, "config.toml"),
		"[plan.guardrails.dry]\ndescription = \"Reuse existing helpers.\"\nseverity = \"error\"\n")

	candidatesJSON := `[{"id":"other-rule","description":"A different rule.","severity":"warning"}]`
	out, err := validate(root, ValidateIn{Action: "guardrails", CandidatesJSON: candidatesJSON})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if len(out.Findings) != 0 {
		t.Fatalf("expected no findings for a new id, got %+v", out.Findings)
	}
}

// TestValidateGuardrailsCandidates_SeverityDowngrade_NoCandidatesNoCheck checks
// that the guardrails action with no candidatesJson returns no findings.
func TestValidateGuardrailsCandidates_SeverityDowngrade_NoCandidatesNoCheck(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, paths.DataDir, "config.toml"),
		"[plan.guardrails.dry]\ndescription = \"Reuse existing helpers.\"\nseverity = \"warning\"\n")

	out, err := validate(root, ValidateIn{Action: "guardrails"})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if len(out.Findings) != 0 {
		t.Fatalf("expected no findings without candidatesJson, got %+v", out.Findings)
	}
}

// TestValidateGuardrailsCandidates_SeverityDowngrade_MissingSectionNoFinding
// checks that a warning candidate gets no downgrade finding when no config file
// exists on disk.
func TestValidateGuardrailsCandidates_SeverityDowngrade_MissingSectionNoFinding(t *testing.T) {
	root := t.TempDir() // no config file: nothing on disk to downgrade

	candidatesJSON := `[{"id":"dry","description":"Reuse existing helpers.","severity":"warning"}]`
	out, err := validate(root, ValidateIn{Action: "guardrails", CandidatesJSON: candidatesJSON})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if len(out.Findings) != 0 {
		t.Fatalf("expected no findings for a missing section, got %+v", out.Findings)
	}
}

// A candidate that both fails validation and lowers severity returns both
// findings: the downgrade check adds to validateOneGuardrail, it does not
// replace it.
func TestValidateGuardrailsCandidates_SeverityDowngrade_AddsToOtherFindings(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, paths.DataDir, "config.toml"),
		"[plan.guardrails.dry]\ndescription = \"Reuse existing helpers.\"\nseverity = \"error\"\n")

	candidatesJSON := `[{"id":"dry","description":"","severity":"warning"}]`
	out, err := validate(root, ValidateIn{Action: "guardrails", CandidatesJSON: candidatesJSON})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if len(out.Findings) != 2 {
		t.Fatalf("expected 2 findings (empty description + downgrade), got %+v", out.Findings)
	}
	var sawDowngrade bool
	for _, f := range out.Findings {
		if strings.Contains(f.Message, "severity lowered from error to warning") {
			sawDowngrade = true
		}
	}
	if !sawDowngrade {
		t.Errorf("no downgrade finding in %+v", out.Findings)
	}
}

// TestGuardrailSeverityDowngrades_TwoIDs checks that two downgraded ids each
// get their own finding that names only that id, in the order of the
// replacement pairs (disk order), and that a kept severity gets none.
func TestGuardrailSeverityDowngrades_TwoIDs(t *testing.T) {
	reps := []guardrailReplacement{
		{Disk: map[string]any{"id": "alpha", "severity": "error"}, Candidate: map[string]any{"id": "alpha", "severity": "warning"}},
		{Disk: map[string]any{"id": "beta", "severity": "error"}, Candidate: map[string]any{"id": "beta", "severity": "error"}},
		{Disk: map[string]any{"id": "gamma"}, Candidate: map[string]any{"id": "gamma", "severity": "warning"}},
	}
	findings := guardrailSeverityDowngrades(reps)
	wantIDs := []string{"alpha", "gamma"}
	if len(findings) != len(wantIDs) {
		t.Fatalf("got %d findings, want %d: %+v", len(findings), len(wantIDs), findings)
	}
	for i, id := range wantIDs {
		f := findings[i]
		wantMsg := id + ": severity lowered from error to warning (harden is strengthen-only)"
		if f.ID != id || f.Message != wantMsg || f.Severity != "error" || f.Fix != guardrailDowngradeFix {
			t.Errorf("finding %d = %+v, want id %q, message %q, severity error", i, f, id, wantMsg)
		}
		for _, other := range []string{"alpha", "beta", "gamma"} {
			if other != id && strings.Contains(f.Message, other) {
				t.Errorf("finding %d message %q names another id %q", i, f.Message, other)
			}
		}
	}

	t.Run("through validate", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, filepath.Join(root, paths.DataDir, "config.toml"),
			"[plan.guardrails.alpha]\ndescription = \"Rule alpha.\"\nseverity = \"error\"\n\n"+
				"[plan.guardrails.beta]\ndescription = \"Rule beta.\"\nseverity = \"error\"\n\n"+
				"[plan.guardrails.gamma]\ndescription = \"Rule gamma.\"\nseverity = \"error\"\n")
		// Candidates in reverse order: the findings still follow disk order.
		candidatesJSON := `[{"id":"gamma","description":"Rule gamma.","severity":"warning"},` +
			`{"id":"beta","description":"Rule beta.","severity":"error"},` +
			`{"id":"alpha","description":"Rule alpha.","severity":"warning"}]`
		out, err := validate(root, ValidateIn{Action: "guardrails", CandidatesJSON: candidatesJSON})
		if err != nil {
			t.Fatalf("validate: %v", err)
		}
		var gotIDs []string
		for _, f := range out.Findings {
			gotIDs = append(gotIDs, f.ID)
		}
		if !reflect.DeepEqual(gotIDs, wantIDs) {
			t.Fatalf("finding ids = %v, want %v (findings %+v)", gotIDs, wantIDs, out.Findings)
		}
	})
}

// TestMergeGuardrailCandidates_ReturnsReplacedPairs checks that
// mergeGuardrailCandidates returns one disk and candidate pair for each
// replaced id, and no pair when candidatesJson is empty.
func TestMergeGuardrailCandidates_ReturnsReplacedPairs(t *testing.T) {
	raw := []any{
		map[string]any{"id": "a", "severity": "error"},
		map[string]any{"id": "b", "severity": "warning"},
	}
	merged, reps, err := mergeGuardrailCandidates(raw,
		`[{"id":"b","severity":"error"},{"id":"c","severity":"warning"}]`)
	if err != nil {
		t.Fatalf("mergeGuardrailCandidates: %v", err)
	}
	if len(merged) != 3 {
		t.Fatalf("merged = %+v, want 3 entries (a, b replaced, c added)", merged)
	}
	if len(reps) != 1 {
		t.Fatalf("reps = %+v, want exactly 1 replaced pair (b)", reps)
	}
	if reps[0].Disk["severity"] != "warning" || reps[0].Candidate["severity"] != "error" {
		t.Errorf("pair = %+v, want Disk warning and Candidate error", reps[0])
	}

	_, noReps, err := mergeGuardrailCandidates(raw, "")
	if err != nil {
		t.Fatalf("mergeGuardrailCandidates (no candidates): %v", err)
	}
	if len(noReps) != 0 {
		t.Errorf("reps without candidatesJson = %+v, want none", noReps)
	}
}

// ---------------------------------------------------------------------------
// dimensions
// ---------------------------------------------------------------------------

func TestValidateDimensionsUnreadableDirIsInfraError(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, paths.DataDir, "review-dimensions"), "not a directory")

	_, err := validate(root, ValidateIn{Action: "dimensions"})
	var infra *mcpserver.InfraError
	if !errors.As(err, &infra) {
		t.Fatalf("err = %v, want *mcpserver.InfraError", err)
	}
}

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
	if err := os.WriteFile(filepath.Join(root, ".github", "workflows", "release-on-main.yml"), []byte("# release-on-main-version: 1\n"), 0644); err != nil {
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

func TestLinksValidateParentheses(t *testing.T) {
	cases := []struct {
		name string
		line string
		want string
	}{
		{"wikipedia-style balanced parens", "See https://en.wikipedia.org/wiki/Foo_(bar) here.", "https://en.wikipedia.org/wiki/Foo_(bar)"},
		{"markdown link", "Read [the docs](https://a.b/c).", "https://a.b/c"},
		{"balanced parens inside a markdown link", "Read [x](https://en.wikipedia.org/wiki/Foo_(bar)).", "https://en.wikipedia.org/wiki/Foo_(bar)"},
		{"balanced parens inside plain parens", "(see https://en.wikipedia.org/wiki/Foo_(bar)).", "https://en.wikipedia.org/wiki/Foo_(bar)"},
		{"adjacent markdown links", "[a](https://x.y/1),[b](https://x.y/2)", "https://x.y/1"},
		{"unbalanced open paren", "https://x.y/a(b", "https://x.y/a(b"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeFile(t, filepath.Join(root, "doc.md"), tc.line+"\n")
			out, err := linksValidate(root, LinksValidateIn{File: "doc.md", Offline: true})
			if err != nil {
				t.Fatalf("linksValidate: %v", err)
			}
			if len(out.Results) == 0 {
				t.Fatalf("no URL extracted from %q", tc.line)
			}
			if got := out.Results[0].URL; got != tc.want {
				t.Errorf("URL = %q, want %q", got, tc.want)
			}
		})
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
	if !strings.Contains(string(data), " — mcp-failure[auth]: test_tool\n") {
		t.Errorf("log.md missing expected heading, got: %s", data)
	}
	if strings.Contains(string(data), "jira") {
		t.Errorf("heading for the non-Jira tool test_tool must not mention jira, got: %s", data)
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

// TestStrayStateAcceptsLinks pins F-worktree-state-links-7: a linked-state
// entry (paths.LinkedStateEntries) that is a real symlink into the main
// worktree's .sdlc-v2/ is not stray -- it is the mechanism by which a linked
// worktree is meant to see run state.
func TestStrayStateAcceptsLinks(t *testing.T) {
	mainRoot := t.TempDir()
	activeRoot := t.TempDir()

	mainDataDir := filepath.Join(mainRoot, paths.DataDir)
	if err := os.MkdirAll(filepath.Join(mainDataDir, paths.RunsSubdir), 0o755); err != nil {
		t.Fatal(err)
	}

	activeDataDir := filepath.Join(activeRoot, paths.DataDir)
	if err := os.MkdirAll(activeDataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(mainDataDir, paths.RunsSubdir), filepath.Join(activeDataDir, paths.RunsSubdir)); err != nil {
		t.Fatal(err)
	}

	findings, err := findStrayStateEntries(mainRoot, activeRoot)
	if err != nil {
		t.Fatalf("findStrayStateEntries: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("expected no findings for a correctly-targeted symlink, got %+v", findings)
	}
}

// TestStrayStateWrongLinkTarget pins F-worktree-state-links-8: a
// linked-state-named entry that is a symlink, but points somewhere other
// than the main worktree's .sdlc-v2/<name>, is still reported stray --
// naming the entry in the finding -- because it is not the link the
// mechanism is supposed to create.
func TestStrayStateWrongLinkTarget(t *testing.T) {
	mainRoot := t.TempDir()
	activeRoot := t.TempDir()
	elsewhere := t.TempDir()

	if err := os.MkdirAll(filepath.Join(mainRoot, paths.DataDir), 0o755); err != nil {
		t.Fatal(err)
	}
	activeDataDir := filepath.Join(activeRoot, paths.DataDir)
	if err := os.MkdirAll(activeDataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, filepath.Join(activeDataDir, paths.RunsSubdir)); err != nil {
		t.Fatal(err)
	}

	findings, err := findStrayStateEntries(mainRoot, activeRoot)
	if err != nil {
		t.Fatalf("findStrayStateEntries: %v", err)
	}
	strayFindings := findingsByID(findings, "WORKTREE_ANCHOR_STRAY_STATE")
	if len(strayFindings) != 1 {
		t.Fatalf("expected 1 WORKTREE_ANCHOR_STRAY_STATE finding for a symlink pointing elsewhere, got %d: %+v", len(strayFindings), findings)
	}
	if !strings.Contains(strayFindings[0].Message, paths.RunsSubdir) {
		t.Errorf("message = %q, want mention of the stray entry name %q", strayFindings[0].Message, paths.RunsSubdir)
	}
}

// TestStrayStateAcceptsDanglingLink pins the worktree-state-links spec's
// "Link creation trigger" requirement: SessionStart creates a linked-state
// symlink even when its main-worktree target does not exist yet (dangling
// until the first write), so the stray-state check must not flag that link
// as stray just because the target is missing.
func TestStrayStateAcceptsDanglingLink(t *testing.T) {
	mainRoot := t.TempDir()
	activeRoot := t.TempDir()

	// mainRoot/.sdlc-v2/ is never created here -- the symlink's target,
	// mainRoot/.sdlc-v2/runs, does not exist at all yet.
	activeDataDir := filepath.Join(activeRoot, paths.DataDir)
	if err := os.MkdirAll(activeDataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(mainRoot, paths.DataDir, paths.RunsSubdir)
	if err := os.Symlink(target, filepath.Join(activeDataDir, paths.RunsSubdir)); err != nil {
		t.Fatal(err)
	}

	findings, err := findStrayStateEntries(mainRoot, activeRoot)
	if err != nil {
		t.Fatalf("findStrayStateEntries: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("expected no findings for a dangling but correctly-targeted symlink, got %+v", findings)
	}
}

// TestValidateCostTiers_ErrorsByCause pins the two recovery paths of
// validateCostTiers: an unreadable docs/cost-tiers.md must not be
// reported as a heading problem, and neither message may print the file path
// twice.
func TestValidateCostTiers_ErrorsByCause(t *testing.T) {
	cases := []struct {
		name        string
		doc         string // "" means: put a directory where the file should be (unreadable)
		wantHint    string
		notWantHint string
	}{
		{
			name:        "unreadable file",
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
			} else if err := os.MkdirAll(filepath.Join(root, "docs", "cost-tiers.md"), 0o755); err != nil {
				t.Fatal(err)
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
		{"PF13 style limits", checkPF13(commstyle.FromSections(nil, nil), commstyle.Report{
			Sections:   []commstyle.SectionMetrics{{Name: "Context", Failures: []string{"prose share 0.65 > 0.30"}}},
			BannedHits: []commstyle.BannedHit{{Phrase: "great question", Line: 88}},
			SteHits:    []commstyle.SteHit{{Rule: "ing-form", Text: "starting", Line: 92}},
		}), true},
		{"PF14 diagram contrast", checkPF14("```mermaid\nclassDef new fill:#d4f7d4\n```\n"), true},
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
	for _, id := range []string{"PF1", "PF2", "PF3", "PF4", "PF5", "PF6", "PF7", "PF9", "PF10", "PF11", "PF12", "PF13", "PF14"} {
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

// ---------------------------------------------------------------------------
// plan_format PF13/PF14 and the plan_style action
// ---------------------------------------------------------------------------

// proseContext is a Context section of 44 prose words and no visual lines,
// so its prose share is 1.00.
const proseContext = `## Context

The plan changes how the tool reads the style. It keeps the old values for now.

The team asked for this change last week. We want the output to be short and clear for every reader. Each section uses a table where it can.
`

// writeLocalToml writes the developer-local config the style is read from.
func writeLocalToml(t *testing.T, root, content string) {
	t.Helper()
	writeFile(t, filepath.Join(root, paths.DataDir, "local.toml"), content)
}

// lineOf returns the 1-based line of the first line of content that
// contains substr, or 0.
func lineOf(content, substr string) int {
	for i, l := range strings.Split(content, "\n") {
		if strings.Contains(l, substr) {
			return i + 1
		}
	}
	return 0
}

// runValidate calls validate and fails the test on an error.
func runValidate(t *testing.T, root string, in ValidateIn) ValidateOut {
	t.Helper()
	out, err := validate(root, in)
	if err != nil {
		t.Fatalf("validate(%s): %v", in.Action, err)
	}
	return out
}

// onlyFinding returns the single finding with id, failing the test when
// there is not exactly one.
func onlyFinding(t *testing.T, findings []discovery.Finding, id string) discovery.Finding {
	t.Helper()
	got := findingsByID(findings, id)
	if len(got) != 1 {
		t.Fatalf("want exactly one %s finding, got %d: %+v", id, len(got), findings)
	}
	return got[0]
}

func TestValidatePlanFormat_PF13ProseShareFails(t *testing.T) {
	root := t.TempDir()
	writeLocalToml(t, root, "[planStyle]\nvisualDensity = \"high\"\n")
	writeFile(t, filepath.Join(root, "plan.md"), goodPlan+"\n"+proseContext)

	f := onlyFinding(t, runValidate(t, root, ValidateIn{Action: "plan_format", File: "plan.md"}).Findings, "PF13")
	if f.Severity != "error" {
		t.Errorf("severity = %q, want error", f.Severity)
	}
	if !strings.HasPrefix(f.Message, "Plan style limits from [style] and [planStyle] not met (visualDensity=high,") {
		t.Errorf("headline missing or wrong:\n%s", f.Message)
	}
	if !strings.Contains(f.Message, "\n- Context: prose share 1.00 > 0.30") {
		t.Errorf("message lacks the Context prose-share line:\n%s", f.Message)
	}
	if f.Fix == "" {
		t.Error("PF13 fix is empty")
	}
}

func TestValidatePlanFormat_PF13BannedPhrase(t *testing.T) {
	root := t.TempDir()
	plan := goodPlan + "\n## Notes\n\nGreat question about the layout.\n"
	writeFile(t, filepath.Join(root, "plan.md"), plan)

	f := onlyFinding(t, runValidate(t, root, ValidateIn{Action: "plan_format", File: "plan.md"}).Findings, "PF13")
	want := fmt.Sprintf("- line %d: banned phrase \"great question\"", lineOf(plan, "Great question"))
	if !strings.Contains(f.Message, want) {
		t.Errorf("message lacks %q:\n%s", want, f.Message)
	}
}

func TestValidatePlanFormat_PF13SteHit(t *testing.T) {
	root := t.TempDir()
	writeLocalToml(t, root, "[style]\nwritingStandard = \"ste\"\n")
	plan := goodPlan + "\n## Notes\n\nBefore starting the tool, read the guide.\n"
	writeFile(t, filepath.Join(root, "plan.md"), plan)

	f := onlyFinding(t, runValidate(t, root, ValidateIn{Action: "plan_format", File: "plan.md"}).Findings, "PF13")
	want := fmt.Sprintf("- line %d: STE ing-form: \"starting\"", lineOf(plan, "Before starting"))
	if !strings.Contains(f.Message, want) {
		t.Errorf("message lacks %q:\n%s", want, f.Message)
	}
	if !strings.Contains(f.Message, "writingStandard=ste") {
		t.Errorf("headline does not name writingStandard=ste:\n%s", f.Message)
	}
}

func TestValidatePlanFormat_PF13ShortSectionSkipped(t *testing.T) {
	root := t.TempDir()
	writeLocalToml(t, root, "[planStyle]\nvisualDensity = \"high\"\n")
	// 25 words of prose: under the 40-word floor, so it is skipped.
	short := "## Final Shape\n\nThe tool reads one config file and writes one report. Each run is short. The team reviews the report and the plan before each merge.\n"
	writeFile(t, filepath.Join(root, "plan.md"), goodPlan+"\n"+short)

	out := runValidate(t, root, ValidateIn{Action: "plan_format", File: "plan.md"})
	for _, f := range findingsByID(out.Findings, "PF13") {
		if strings.Contains(f.Message, "Final Shape") {
			t.Errorf("PF13 names the skipped Final Shape section:\n%s", f.Message)
		}
	}

	style := runValidate(t, root, ValidateIn{Action: "plan_style", File: "plan.md"}).StyleReport
	for _, s := range style.Sections {
		if s.Name == "Final Shape" && s.Status != "skipped" {
			t.Errorf("Final Shape status = %q (words=%d), want skipped", s.Status, s.Words)
		}
	}
}

// TestValidatePlanFormat_PF13TemplateSections pins that a template limits
// PF13 to its narrative sections: Tasks (not narrative) is not measured even
// though it breaks the prose limit.
func TestValidatePlanFormat_PF13TemplateSections(t *testing.T) {
	root := t.TempDir()
	writeLocalToml(t, root, "[planStyle]\nvisualDensity = \"high\"\n")
	writeFile(t, filepath.Join(root, "template.md"), "# T\n\n## Required Sections\n\n- Tasks\n- Context <!-- narrative: true -->\n")
	tasks := strings.Replace(proseContext, "## Context", "## Tasks", 1)
	writeFile(t, filepath.Join(root, "plan.md"), goodPlan+"\n"+proseContext+"\n"+tasks)

	f := onlyFinding(t, runValidate(t, root, ValidateIn{Action: "plan_format", File: "plan.md", Template: "template.md"}).Findings, "PF13")
	if !strings.Contains(f.Message, "- Context: prose share") {
		t.Errorf("PF13 does not measure Context:\n%s", f.Message)
	}
	if strings.Contains(f.Message, "- Tasks:") {
		t.Errorf("PF13 measures the non-narrative Tasks section:\n%s", f.Message)
	}

	rep := runValidate(t, root, ValidateIn{Action: "plan_style", File: "plan.md", Template: "template.md"}).StyleReport
	if len(rep.Sections) != 1 || rep.Sections[0].Name != "Context" {
		t.Errorf("styleReport.sections = %+v, want exactly one row named Context", rep.Sections)
	}
}

func TestValidatePlanFormat_PF13UnreadableTemplateErrors(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "plan.md"), goodPlan)

	_, err := validate(root, ValidateIn{Action: "plan_format", File: "plan.md", Template: "missing.md"})
	var de *mcpserver.DomainError
	if !errors.As(err, &de) || !strings.Contains(de.Msg, "template not found") {
		t.Fatalf("err = %v, want DomainError template not found (template is read for PF13 on every call)", err)
	}
}

// TestValidatePlanFormat_HookSkipsPF13 uses a plan that breaks the prose
// limit and also fails PF6, so the hook does compute blocking findings; PF13
// must still be absent from both slices.
func TestValidatePlanFormat_HookSkipsPF13(t *testing.T) {
	root := t.TempDir()
	writeLocalToml(t, root, "[planStyle]\nvisualDensity = \"high\"\n")
	writeFile(t, filepath.Join(root, "plan.md"), planBlockedByPF6()+"\n"+proseContext)

	if len(findingsByID(runValidate(t, root, ValidateIn{Action: "plan_format", File: "plan.md"}).Findings, "PF13")) != 1 {
		t.Fatal("fixture must fail PF13 under plan_format, or the hook check proves nothing")
	}
	blocking, final, err := ValidatePlanFormatForHook(root, "plan.md")
	if err != nil {
		t.Fatalf("hook: %v", err)
	}
	if len(findingsByID(blocking, "PF6")) != 1 {
		t.Fatalf("fixture must block on PF6, got %+v", blocking)
	}
	if len(findingsByID(blocking, "PF13")) != 0 || len(findingsByID(final, "PF13")) != 0 {
		t.Errorf("hook reported PF13: blocking=%+v final=%+v", blocking, final)
	}
}

const shippedClassDefs = "classDef new fill:#1f7a3a,stroke:#0b3d1c,color:#ffffff,stroke-width:2px\n" +
	"    classDef changed fill:#8a6d00,stroke:#4a3a00,color:#ffffff,stroke-width:2px\n"

func mermaidPlan(body string) string {
	return goodPlan + "\n## Final Shape\n\n```mermaid\nflowchart LR\n    A --> B\n    " + body + "```\n"
}

func TestValidatePlanFormat_PF14PastelClassDefFails(t *testing.T) {
	root := t.TempDir()
	plan := mermaidPlan("classDef new fill:#d4f7d4,stroke:#2a7a2a\n")
	writeFile(t, filepath.Join(root, "plan.md"), plan)

	f := onlyFinding(t, runValidate(t, root, ValidateIn{Action: "plan_format", File: "plan.md"}).Findings, "PF14")
	want := fmt.Sprintf("\n- line %d: no text color: classDef new fill:#d4f7d4,stroke:#2a7a2a", lineOf(plan, "#d4f7d4"))
	if !strings.Contains(f.Message, want) {
		t.Errorf("message lacks %q:\n%s", want, f.Message)
	}
	if !strings.Contains(f.Fix, "classDef new fill:#1f7a3a,stroke:#0b3d1c,color:#ffffff,stroke-width:2px") ||
		!strings.Contains(f.Fix, "classDef changed fill:#8a6d00,stroke:#4a3a00,color:#ffffff,stroke-width:2px") {
		t.Errorf("fix does not quote the two exact classDefs:\n%s", f.Fix)
	}
}

func TestValidatePlanFormat_PF14ShippedClassDefsPass(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "plan.md"), mermaidPlan(shippedClassDefs))

	if got := findingsByID(runValidate(t, root, ValidateIn{Action: "plan_format", File: "plan.md"}).Findings, "PF14"); len(got) != 0 {
		t.Errorf("shipped classDefs reported PF14: %+v", got)
	}
}

func TestValidatePlanFormat_PF14LowContrastStyleFails(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "plan.md"), mermaidPlan("style A fill:#fff3c4,color:#ffffff\n"))

	f := onlyFinding(t, runValidate(t, root, ValidateIn{Action: "plan_format", File: "plan.md"}).Findings, "PF14")
	if !regexp.MustCompile(`- line \d+: contrast \d+\.\d:1 < 4\.5:1: style A fill:#fff3c4,color:#ffffff`).MatchString(f.Message) {
		t.Errorf("message lacks the contrast line:\n%s", f.Message)
	}
}

func TestDiagramContrastFindings(t *testing.T) {
	got := DiagramContrastFindings("plan.md", "classDef new fill:#d4f7d4,stroke:#2a7a2a")
	if len(got) != 1 || got[0].ID != "PF14" || got[0].Path != "plan.md" {
		t.Fatalf("pastel classDef without a fence: got %+v, want one PF14 finding", got)
	}
	if !strings.Contains(got[0].Message, "\n- line 1: no text color: classDef new fill:#d4f7d4,stroke:#2a7a2a") {
		t.Errorf("message = %q", got[0].Message)
	}

	clean := DiagramContrastFindings("plan.md", "fix a typo in the intro\n"+shippedClassDefs)
	if clean == nil || len(clean) != 0 {
		t.Errorf("clean text: got %#v, want a non-nil empty slice", clean)
	}
}

func TestValidatePlanStyle_MissingFile(t *testing.T) {
	_, err := validate(t.TempDir(), ValidateIn{Action: "plan_style"})
	var de *mcpserver.DomainError
	if !errors.As(err, &de) {
		t.Fatalf("err = %v, want *mcpserver.DomainError", err)
	}
	if want := "plan_style: file is required"; de.Msg != want {
		t.Errorf("msg = %q, want %q", de.Msg, want)
	}
	if want := "Pass file: the path to the plan .md file, absolute or relative to the project root."; de.Suggestion != want {
		t.Errorf("suggestion = %q, want %q", de.Suggestion, want)
	}
}

// TestValidatePlanStyle_FileNotFound verifies the error names plan_style,
// not plan_format: readPlanFile/planReadError serve both actions, so the
// action name must be threaded through rather than hardcoded.
func TestValidatePlanStyle_FileNotFound(t *testing.T) {
	_, err := validate(t.TempDir(), ValidateIn{Action: "plan_style", File: "missing-plan.md"})
	var de *mcpserver.DomainError
	if !errors.As(err, &de) {
		t.Fatalf("err = %v, want *mcpserver.DomainError", err)
	}
	if !strings.HasPrefix(de.Msg, "plan_style: file not found:") {
		t.Errorf("Msg = %q, want prefix %q", de.Msg, "plan_style: file not found:")
	}
}

// TestValidatePlanStyle_PassReturnsReport pins that a passing plan still
// gets a full report with every list field [] (never null).
func TestValidatePlanStyle_PassReturnsReport(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "template.md"), "# T\n\n## Required Sections\n\n- Context <!-- narrative: true -->\n")
	visual := "## Context\n\n| Field | Before | After |\n|---|---|---|\n" +
		"| audience | free text in the plan config | one of five checked values with a default |\n" +
		"| tone | free text in the plan config | direct or neutral, checked on every read |\n" +
		"| density | not present in the config | high, balanced or low, with numeric limits |\n"
	writeFile(t, filepath.Join(root, "plan.md"), goodPlan+"\n"+visual)

	out := runValidate(t, root, ValidateIn{Action: "plan_style", File: "plan.md", Template: "template.md"})
	if len(out.Findings) != 0 {
		t.Errorf("findings = %+v, want none", out.Findings)
	}
	rep := out.StyleReport
	if rep == nil {
		t.Fatal("styleReport is nil on a passing plan")
	}
	if len(rep.Sections) != 1 || rep.Sections[0].Status != "pass" {
		t.Errorf("sections = %+v, want one Context row with status pass", rep.Sections)
	}
	for _, k := range []string{"audience", "writingStandard", "tone", "visualDensity", "language"} {
		if rep.Settings[k] == "" {
			t.Errorf("settings[%q] is empty", k)
		}
	}
	if len(rep.Settings) != 5 {
		t.Errorf("settings = %v, want exactly 5 keys", rep.Settings)
	}
	raw, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "null") {
		t.Errorf("plan_style output carries null: %s", raw)
	}
}

func TestValidatePlanStyle_FailReturnsReport(t *testing.T) {
	root := t.TempDir()
	writeLocalToml(t, root, "[style]\nwritingStandard = \"ste\"\n[planStyle]\nvisualDensity = \"high\"\n")
	plan := mermaidPlan("classDef new fill:#d4f7d4,stroke:#2a7a2a\n") + "\n" + proseContext + "\nBefore starting the tool, read the guide.\n"
	writeFile(t, filepath.Join(root, "plan.md"), plan)

	out := runValidate(t, root, ValidateIn{Action: "plan_style", File: "plan.md"})
	if len(findingsByID(out.Findings, "PF13")) != 1 || len(out.Findings) != 1 {
		t.Errorf("findings = %+v, want exactly one PF13 (PF14 goes in styleReport only)", out.Findings)
	}
	rep := out.StyleReport
	if rep == nil {
		t.Fatal("styleReport is nil on a failing plan")
	}
	if rep.Settings["writingStandard"] != "ste" || !rep.Limits.STE {
		t.Errorf("settings/limits do not reflect ste: %v %+v", rep.Settings, rep.Limits)
	}
	wantLine := lineOf(plan, "Before starting")
	found := false
	for _, h := range rep.SteHits {
		if h.Rule == "ing-form" && h.Text == "starting" && h.Line == wantLine {
			found = true
		}
	}
	if !found {
		t.Errorf("steHits = %+v, want ing-form \"starting\" at line %d", rep.SteHits, wantLine)
	}

	// diagramContrast lists the same hits plan_format reports as PF14.
	pf14 := onlyFinding(t, runValidate(t, root, ValidateIn{Action: "plan_format", File: "plan.md"}).Findings, "PF14")
	if len(rep.DiagramContrast) != 1 {
		t.Fatalf("diagramContrast = %+v, want one hit", rep.DiagramContrast)
	}
	h := rep.DiagramContrast[0]
	if want := fmt.Sprintf("- line %d: %s: %s", h.Line, h.Reason, h.Text); !strings.Contains(pf14.Message, want) {
		t.Errorf("PF14 message lacks the styleReport hit %q:\n%s", want, pf14.Message)
	}
}

func TestValidatePlanStyle_Instructions(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "plan.md"), goodPlan)

	if got := runValidate(t, root, ValidateIn{Action: "plan_style", File: "plan.md"}).StyleReport.Instructions; got == nil || len(got) != 0 {
		t.Errorf("instructions with no config = %#v, want []", got)
	}

	writeLocalToml(t, root, "[planStyle]\ninstructions = [\"A\"]\n")
	got := runValidate(t, root, ValidateIn{Action: "plan_style", File: "plan.md"}).StyleReport.Instructions
	if len(got) != 1 || got[0] != "A" {
		t.Errorf("instructions = %#v, want [\"A\"] (read fresh on every call)", got)
	}
}

// TestValidatePlanStyle_ConfigReadErrorWarns verifies a malformed
// local.toml does not fail the call: the style falls back to defaults and
// the read error reaches styleReport.warnings.
func TestValidatePlanStyle_ConfigReadErrorWarns(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "plan.md"), goodPlan)
	writeLocalToml(t, root, "[style\naudience = [\n")

	rep := runValidate(t, root, ValidateIn{Action: "plan_style", File: "plan.md"}).StyleReport
	if rep == nil {
		t.Fatal("styleReport = nil, want a report despite the read error")
	}
	if got := rep.Settings["audience"]; got != "functional" {
		t.Errorf("audience = %q, want functional (default)", got)
	}
	found := false
	for _, w := range rep.Warnings {
		if strings.Contains(w, "Failed to read style config: ") {
			found = true
		}
	}
	if !found {
		t.Errorf("warnings = %v, want a \"Failed to read style config: \" entry", rep.Warnings)
	}
}

// TestValidateIn_PlanStyleDescriptions pins that the file and template
// field descriptions, and the action enum, name plan_style.
func TestValidateIn_PlanStyleDescriptions(t *testing.T) {
	typ := reflect.TypeOf(ValidateIn{})
	for _, name := range []string{"File", "Template"} {
		f, _ := typ.FieldByName(name)
		if !strings.Contains(f.Tag.Get("jsonschema_description"), "plan_style") {
			t.Errorf("ValidateIn.%s description does not name plan_style: %q", name, f.Tag.Get("jsonschema_description"))
		}
	}
	f, _ := typ.FieldByName("Action")
	if !strings.Contains(f.Tag.Get("jsonschema"), "enum=plan_style") {
		t.Errorf("Action enum lacks plan_style: %q", f.Tag.Get("jsonschema"))
	}
}
