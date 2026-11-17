// Package skillcheck pins the flag, the resolve call and the instruction list
// step of plugins/sdlc/skills/pr/SKILL.md. Every unexported identifier below
// is prefixed prSkillFlags.
package skillcheck

import (
	"regexp"
	"strings"
	"testing"
)

const prSkillFlagsFile = "skills/pr/SKILL.md"

var prSkillFlagsHintRe = regexp.MustCompile(`(?m)^argument-hint: "(.*)"$`)

// prSkillFlagsWorkflow returns the text of the skill from "## Workflow" to the
// next "## " heading.
func prSkillFlagsWorkflow(t *testing.T, content string) string {
	t.Helper()
	_, after, ok := strings.Cut(content, "\n## Workflow\n")
	if !ok {
		t.Fatal("pr SKILL.md has no ## Workflow section")
	}
	body, _, _ := strings.Cut(after, "\n## ")
	return body
}

func TestPRSkillFlags(t *testing.T) {
	content := shipSkillsReadFile(t, shipSkillsRepoRoot(t), prSkillFlagsFile)
	workflow := prSkillFlagsWorkflow(t, content)

	t.Run("argument hint", func(t *testing.T) {
		m := prSkillFlagsHintRe.FindStringSubmatch(content)
		if m == nil {
			t.Fatal("argument-hint is missing")
		}
		const want = "[--draft] [--base <branch>] [--auto] [--skip-approval] [--no-instructions]"
		if m[1] != want {
			t.Errorf("argument-hint = %q, want %q", m[1], want)
		}
	})

	t.Run("flags in workflow", func(t *testing.T) {
		if !strings.Contains(workflow, "--no-instructions") {
			t.Error("--no-instructions is not in the Workflow text")
		}
		if !strings.Contains(workflow, "--auto") {
			t.Error("--auto is not in the Workflow text")
		}
	})

	t.Run("--no-instructions and --auto are independent", func(t *testing.T) {
		if !strings.Contains(content, "It is independent\n  of `--auto`: both apply.") {
			t.Error("the pr skill does not say that --no-instructions and --auto are independent")
		}
	})

	t.Run("resolve call before Step 0", func(t *testing.T) {
		heading := strings.Index(workflow, "### Step 0a")
		resolve := strings.Index(workflow, `instructions_resolve({ key: "pr" })`)
		step0 := strings.Index(workflow, "### Step 0 (CONSUME)")
		if heading < 0 || resolve < 0 || step0 < 0 {
			t.Fatalf("heading=%d resolve=%d step0=%d: each must be present", heading, resolve, step0)
		}
		if heading > resolve || resolve > step0 {
			t.Errorf("order wrong: Step 0a heading=%d, resolve call=%d, Step 0=%d", heading, resolve, step0)
		}
		if !strings.Contains(workflow, "Skip this step when `--no-instructions` was passed") {
			t.Error("Step 0a does not skip on --no-instructions")
		}
		if !strings.Contains(workflow, "stop before Step 0") {
			t.Error("Step 0a does not say to stop before Step 0 on a resolve error")
		}
	})

	t.Run("Step 7 after Step 6 with summary line", func(t *testing.T) {
		step6 := strings.Index(workflow, "### Step 6")
		step7 := strings.Index(workflow, "### Step 7")
		if step6 < 0 || step7 < 0 || step7 < step6 {
			t.Fatalf("Step 6=%d Step 7=%d: Step 7 must follow Step 6", step6, step7)
		}
		for _, want := range []string{
			"../ship/instructions-run.md",
			"Loop without run state",
			"Instructions (pr): N total, X done, Y failed, Z not run.",
			"Run the remaining instructions by hand. A new run of the skill runs the whole list again unless you pass --no-instructions.",
		} {
			if !strings.Contains(workflow[step7:], want) {
				t.Errorf("Step 7 is missing %q", want)
			}
		}
	})

	t.Run("lease error runs no list", func(t *testing.T) {
		const lease = "remote branch <branch> changed since the last push. pr stopped."
		if strings.Count(content, lease) < 2 {
			t.Errorf("the lease error text %q must be in Step 7 and in Error Recovery", lease)
		}
		step7 := workflow[strings.Index(workflow, "### Step 7"):]
		if !strings.Contains(step7, "A cancel at Step 5 or a tool error in") {
			t.Error("Step 7 does not name the routes that run no list")
		}
	})

	t.Run("git rule names the instruction exception", func(t *testing.T) {
		for _, want := range []string{
			"the skill runs no git or gh commands itself, except an instruction from config",
			"The only exception is an instruction from config (Step 7).",
		} {
			if !strings.Contains(content, want) {
				t.Errorf("git rule text missing: %q", want)
			}
		}
	})

	t.Run("What's Next line", func(t *testing.T) {
		if !strings.Contains(content, "the `Instructions (pr): …` line names each failed or skipped instruction") {
			t.Error("What's Next has no line for the summary")
		}
	})
}

// TestPRApprovedDraftBlock pins the Approved PR draft rule: the block heading,
// the skip of Steps 2 to 4, the invalid-block fallback and the invocation-only scope.
func TestPRApprovedDraftBlock(t *testing.T) {
	content := shipSkillsReadFile(t, shipSkillsRepoRoot(t), prSkillFlagsFile)
	workflow := prSkillFlagsWorkflow(t, content)

	rule := strings.Index(workflow, "### Step 1c")
	step2 := strings.Index(workflow, "### Step 2 (PLAN)")
	if rule < 0 || step2 < 0 || rule > step2 {
		t.Fatalf("Step 1c=%d Step 2=%d: the rule must come before Step 2", rule, step2)
	}
	section := workflow[rule:step2]

	for _, want := range []string{
		"Approved PR draft (publish unchanged):",
		"title: <approved title>",
		"body:",
		"releaseNotes: <approved notes, or \"draft them\">",
		"The block counts only in the invocation prompt.",
		"| valid | skipped | the block text, unchanged |",
		"| no `title:` line, or an empty body | run, after one warning line | the drafted text |",
		"byte for byte",
		"Skip Steps 2, 3 and 4",
		"Approved PR draft block is not valid",
		"`pr_apply` still removes an AI\n  attribution line",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("Step 1c is missing %q", want)
		}
	}

	if !strings.Contains(content, "Change the title or the body of a valid approved draft") {
		t.Error("DO NOT list does not forbid a change to an approved draft")
	}
}
