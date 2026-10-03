package hooks

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/rnagrodzki/sdlc-plugin/internal/discovery"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
)

// These tests cover the plan branch of post-tool-validate: the block reason is
// made of self-contained finding blocks, and PF9/PF10 are previewed under
// "will fail at --final:" only when something already blocks. The helpers
// (triggerEvent, mustBlock, assertSilent, chdir, realPath, mustMkdirAll,
// mustWriteFile) live in gate_hooks_test.go.

// hookPlanFailsPF7 has a Create bullet and no Contract block, so PF7 and PF12
// block. It has no "## Verification Scorecard", so PF9 is a final-only failure.
const hookPlanFailsPF7 = `**Goal:** Do the thing
**Architecture:** Some arch
**Source:** origin
**Verification:** tests

### Task 1: Implement feature

**Complexity:** Standard
**Risk:** Low
**Depends on:** none
**Verify:** tests (go test ./internal/tools/ -run TestFoo)

**Files:**
- Create: foo.go

**Acceptance criteria:**
- [ ] does the thing

## Deviations & assumptions

None.
`

// hookPlanTemplateRollback is a plan template that requires a section the
// fixture plans do not have.
const hookPlanTemplateRollback = `# Plan Template

## Required Sections

- Rollback Plan
`

// planHookFixture chdirs into a fresh temp project, clears CLAUDE_PLUGIN_ROOT so
// only templates written by the test can be found, and writes the plan under
// plans/. It returns the project root and the plan path.
func planHookFixture(t *testing.T, plan string) (dir, planPath string) {
	t.Helper()
	t.Setenv("CLAUDE_PLUGIN_ROOT", "")
	dir = realPath(t, t.TempDir())
	chdir(t, dir)
	plansDir := filepath.Join(dir, "plans")
	mustMkdirAll(t, plansDir)
	planPath = filepath.Join(plansDir, "my-plan.md")
	mustWriteFile(t, planPath, plan)
	return dir, planPath
}

func blockReason(t *testing.T, out Output) string {
	t.Helper()
	payload, ok := out.JSON.(map[string]any)
	if !ok {
		t.Fatalf("JSON = %v (%T), want map[string]any", out.JSON, out.JSON)
	}
	reason, _ := payload["reason"].(string)
	return reason
}

func TestPostToolValidate_Plan_BlockReasonIsSelfContained(t *testing.T) {
	_, planPath := planHookFixture(t, hookPlanFailsPF7)

	out, err := postToolValidate(HookCtx{}, triggerEvent(planPath))
	mustBlock(t, out, err, "PF7: Missing **Contract:** block: Task 1")
	reason := blockReason(t, out)

	// One block per finding, separated by a blank line: PF7, PF12, then the
	// final-only preview.
	blocks := strings.Split(reason, "\n\n")
	if len(blocks) != 3 {
		t.Fatalf("want 3 blank-line-separated blocks (PF7, PF12, final-only), got %d:\n%s", len(blocks), reason)
	}
	for i, id := range []string{"PF7", "PF12"} {
		b := blocks[i]
		if !strings.HasPrefix(b, id+": ") {
			t.Errorf("block %d should start with %q, got:\n%s", i, id+": ", b)
		}
		if !strings.Contains(b, "\n  file: "+planPath) {
			t.Errorf("%s block should name the plan file %s, got:\n%s", id, planPath, b)
		}
		if !strings.Contains(b, "\n  fix:  ") {
			t.Errorf("%s block should carry a fix, got:\n%s", id, b)
		}
	}
	if !strings.Contains(blocks[0], "**Contract:**") || !strings.Contains(blocks[0], "- shape") {
		t.Errorf("PF7 fix should write the Contract shape inline, got:\n%s", blocks[0])
	}

	wantHead := "will fail at --final:\nPF9: "
	if !strings.HasPrefix(blocks[2], wantHead) {
		t.Errorf("last block should start with %q, got:\n%s", wantHead, blocks[2])
	}
	if !strings.Contains(blocks[2], "## Verification Scorecard") {
		t.Errorf("PF9 block should write the scorecard heading, got:\n%s", blocks[2])
	}
	// PF9 is a preview only: it must not appear among the blocking findings.
	if strings.Contains(blocks[0]+blocks[1], "PF9") {
		t.Errorf("PF9 must appear only under the final-only heading:\n%s", reason)
	}
}

// pastelClassDefLine has no "color:" key, so commstyle flags it as "no text
// color" (the same shape as pf14Fix's own example).
const pastelClassDefLine = "classDef new fill:#d4f7d4,stroke:#2a7a2a"

func TestPostToolValidate_Plan_DiagramContrast_EditBlocks(t *testing.T) {
	_, planPath := planHookFixture(t, validPlanFixtureMissingScorecard)

	event := Event{Raw: map[string]any{"tool_input": map[string]any{
		"file_path":  planPath,
		"old_string": "## Deviations & assumptions",
		"new_string": "```mermaid\nflowchart TD\n  A --> B\n  " + pastelClassDefLine + "\n```\n\n## Deviations & assumptions",
	}}}

	out, err := postToolValidate(HookCtx{}, event)
	mustBlock(t, out, err, "PF14: Mermaid diagram colors are hard to read:")
	reason := blockReason(t, out)
	if !strings.Contains(reason, "no text color") {
		t.Errorf("reason should name the contrast problem, got:\n%s", reason)
	}
	if !strings.Contains(reason, pastelClassDefLine) {
		t.Errorf("reason should quote the offending line, got:\n%s", reason)
	}
}

func TestPostToolValidate_Plan_DiagramContrast_UnrelatedEditPasses(t *testing.T) {
	// The plan on disk already has an old, pastel diagram. A typo-fix edit
	// that never touches the classDef line must not block on it.
	planWithOldPastelDiagram := validPlanFixtureMissingScorecard +
		"\n```mermaid\nflowchart TD\n  A --> B\n  " + pastelClassDefLine + "\n```\n"
	_, planPath := planHookFixture(t, planWithOldPastelDiagram)

	event := Event{Raw: map[string]any{"tool_input": map[string]any{
		"file_path":  planPath,
		"old_string": "**Verify:** tests",
		"new_string": "**Verify:** test",
	}}}

	out, err := postToolValidate(HookCtx{}, event)
	if err != nil {
		t.Fatal(err)
	}
	assertSilent(t, out)
}

func TestPostToolValidate_Plan_DiagramContrast_WriteBlocks(t *testing.T) {
	_, planPath := planHookFixture(t, validPlanFixtureMissingScorecard)
	fullPlan := validPlanFixtureMissingScorecard +
		"\n```mermaid\nflowchart TD\n  A --> B\n  " + pastelClassDefLine + "\n```\n"

	event := Event{Raw: map[string]any{"tool_input": map[string]any{
		"file_path": planPath,
		"content":   fullPlan,
	}}}

	out, err := postToolValidate(HookCtx{}, event)
	mustBlock(t, out, err, "PF14: Mermaid diagram colors are hard to read:")
}

func TestPostToolValidate_Plan_NothingBlocking_SilentEvenWithFinalOnlyFailures(t *testing.T) {
	dir, planPath := planHookFixture(t, validPlanFixtureMissingScorecard)
	// PF9 (no scorecard) and PF10 (template section missing) would both fail at
	// --final. With nothing blocking, the hook must still say nothing.
	mustMkdirAll(t, filepath.Join(dir, paths.DataDir))
	mustWriteFile(t, filepath.Join(dir, paths.DataDir, "plan-template.md"), hookPlanTemplateRollback)

	out, err := postToolValidate(HookCtx{}, triggerEvent(planPath))
	if err != nil {
		t.Fatal(err)
	}
	assertSilent(t, out)
}

func TestPostToolValidate_Plan_ProjectTemplate_PreviewsPF10(t *testing.T) {
	dir, planPath := planHookFixture(t, hookPlanFailsPF7)
	tpl := filepath.Join(dir, paths.DataDir, "plan-template.md")
	mustMkdirAll(t, filepath.Dir(tpl))
	mustWriteFile(t, tpl, hookPlanTemplateRollback)

	out, err := postToolValidate(HookCtx{}, triggerEvent(planPath))
	mustBlock(t, out, err, "will fail at --final:")
	reason := blockReason(t, out)

	head := strings.Index(reason, "will fail at --final:")
	pf10 := strings.Index(reason, "PF10: ")
	if pf10 < head {
		t.Fatalf("PF10 should appear under the final-only heading:\n%s", reason)
	}
	preview := reason[head:]
	for _, want := range []string{"PF9: ", "PF10: ", "Rollback Plan", tpl, "  ## Rollback Plan"} {
		if !strings.Contains(preview, want) {
			t.Errorf("final-only preview should contain %q, got:\n%s", want, preview)
		}
	}
}

func TestPostToolValidate_Plan_NoTemplate_PreviewsPF9Only(t *testing.T) {
	_, planPath := planHookFixture(t, hookPlanFailsPF7)

	out, err := postToolValidate(HookCtx{}, triggerEvent(planPath))
	mustBlock(t, out, err, "will fail at --final:\nPF9: ")
	if reason := blockReason(t, out); strings.Contains(reason, "PF10") {
		t.Errorf("PF10 must be skipped when no template resolves, got:\n%s", reason)
	}
}

func TestPostToolValidate_Plan_UnreadableTemplate_StillBlocks(t *testing.T) {
	dir, planPath := planHookFixture(t, hookPlanFailsPF7)
	// A directory where the template file should be: resolving it fails. The
	// hook must still report the blocking findings and PF9, without an error.
	mustMkdirAll(t, filepath.Join(dir, paths.DataDir, "plan-template.md"))

	out, err := postToolValidate(HookCtx{}, triggerEvent(planPath))
	mustBlock(t, out, err, "PF7: ")
	reason := blockReason(t, out)
	if !strings.Contains(reason, "will fail at --final:\nPF9: ") {
		t.Errorf("PF9 should still be previewed, got:\n%s", reason)
	}
	if strings.Contains(reason, "PF10") {
		t.Errorf("PF10 must be skipped when the template cannot be read, got:\n%s", reason)
	}
}

func TestPostToolValidate_Plan_PluginDefaultTemplate_PreviewsPF10(t *testing.T) {
	_, planPath := planHookFixture(t, hookPlanFailsPF7)
	pluginRoot := realPath(t, t.TempDir())
	t.Setenv("CLAUDE_PLUGIN_ROOT", pluginRoot)
	defaultTpl := filepath.Join(pluginRoot, "skills", "plan", "plan-template-default.md")
	mustMkdirAll(t, filepath.Dir(defaultTpl))
	mustWriteFile(t, defaultTpl, hookPlanTemplateRollback)

	out, err := postToolValidate(HookCtx{}, triggerEvent(planPath))
	mustBlock(t, out, err, "PF10: ")
	if reason := blockReason(t, out); !strings.Contains(reason, defaultTpl) {
		t.Errorf("PF10 should name the plugin default template %s, got:\n%s", defaultTpl, reason)
	}
}

func TestJoinValidationFindings_Rendering(t *testing.T) {
	findings := []discovery.Finding{
		{
			ID:      "PF3",
			Message: "Invalid task metadata:\n- Task 1: a\n- Task 2: b",
			Path:    "/p/plan.md",
			Fix:     "line one\n  indented\nline three",
		},
		{ID: "D1", Message: "single line"},
	}

	want := "PF3: Invalid task metadata:\n" +
		"  - Task 1: a\n" +
		"  - Task 2: b\n" +
		"  file: /p/plan.md\n" +
		"  fix:  line one\n" +
		"          indented\n" +
		"        line three\n" +
		"\n" +
		"D1: single line"
	if got := joinValidationFindings(findings); got != want {
		t.Errorf("joinValidationFindings mismatch\n got:\n%s\nwant:\n%s", got, want)
	}
}

func TestJoinValidationFindings_NoTrailingWhitespace(t *testing.T) {
	// A blank line inside a message or fix stays blank instead of gaining indent.
	got := joinValidationFindings([]discovery.Finding{
		{ID: "X1", Message: "head\n\ntail", Path: "/p", Fix: "a\n\nb"},
	})
	for i, line := range strings.Split(got, "\n") {
		if line != strings.TrimRight(line, " \t") {
			t.Errorf("line %d has trailing whitespace: %q", i, line)
		}
	}
}

func TestEditedText(t *testing.T) {
	cases := []struct {
		name string
		in   map[string]any
		want string
	}{
		{
			name: "Edit",
			in:   map[string]any{"file_path": "/p", "old_string": "a", "new_string": "b"},
			want: "b",
		},
		{
			name: "Write",
			in:   map[string]any{"file_path": "/p", "content": "whole file"},
			want: "whole file",
		},
		{
			name: "MultiEdit joins every edits[].new_string with a newline",
			in: map[string]any{"file_path": "/p", "edits": []any{
				map[string]any{"old_string": "a", "new_string": "first"},
				map[string]any{"old_string": "c", "new_string": "second"},
			}},
			want: "first\nsecond",
		},
		{
			name: "other tool (e.g. Bash) has none of the expected keys",
			in:   map[string]any{"command": "echo hi"},
			want: "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := editedText(c.in); got != c.want {
				t.Errorf("editedText(%v) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
