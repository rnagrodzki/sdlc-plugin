// Package skillcheck pins the flags, the resolve call, the push wiring and the
// instruction list step of plugins/sdlc/skills/commit/SKILL.md. Every
// unexported identifier below is prefixed commitSkillFlags.
package skillcheck

import (
	"regexp"
	"strings"
	"testing"
)

const commitSkillFlagsFile = "skills/commit/SKILL.md"

var commitSkillFlagsHintRe = regexp.MustCompile(`(?m)^argument-hint: "(.*)"$`)

// commitSkillFlagsWorkflow returns the text of the skill from "## Workflow" to
// the next "## " heading. A flag must appear in this text, not only in the
// argument hint.
func commitSkillFlagsWorkflow(t *testing.T, content string) string {
	t.Helper()
	_, after, ok := strings.Cut(content, "\n## Workflow\n")
	if !ok {
		t.Fatal("commit SKILL.md has no ## Workflow section")
	}
	body, _, _ := strings.Cut(after, "\n## ")
	return body
}

func TestCommitSkillFlags(t *testing.T) {
	content := shipSkillsReadFile(t, shipSkillsRepoRoot(t), commitSkillFlagsFile)
	workflow := commitSkillFlagsWorkflow(t, content)

	t.Run("argument hint", func(t *testing.T) {
		m := commitSkillFlagsHintRe.FindStringSubmatch(content)
		if m == nil {
			t.Fatal("argument-hint is missing")
		}
		const want = "[--scope <scope>] [--type <type>] [--auto] [--force-default-branch] [--push] [--no-instructions]"
		if m[1] != want {
			t.Errorf("argument-hint = %q, want %q", m[1], want)
		}
	})

	t.Run("flags in workflow", func(t *testing.T) {
		for _, flag := range []string{"--push", "--no-instructions", "--auto", "--force-default-branch"} {
			if !strings.Contains(workflow, flag) {
				t.Errorf("flag %s is not in the Workflow text", flag)
			}
		}
	})

	t.Run("resolve call before Step 0", func(t *testing.T) {
		resolve := strings.Index(workflow, `instructions_resolve({ key: "commit" })`)
		step0 := strings.Index(workflow, "### Step 0 (CONSUME)")
		heading := strings.Index(workflow, "### Step 0a")
		if resolve < 0 || step0 < 0 || heading < 0 {
			t.Fatalf("resolve=%d step0=%d heading=%d: each must be present", resolve, step0, heading)
		}
		if heading > resolve || resolve > step0 {
			t.Errorf("order wrong: Step 0a heading=%d, resolve call=%d, Step 0=%d", heading, resolve, step0)
		}
		if !strings.Contains(workflow, "Skip this step when `--no-instructions` was passed") {
			t.Error("Step 0a does not skip on --no-instructions")
		}
	})

	t.Run("resolve error stops before Step 0", func(t *testing.T) {
		if !strings.Contains(workflow, "stop before Step 0") {
			t.Error("Step 0a does not say to stop before Step 0 on a resolve error")
		}
	})

	t.Run("push wiring", func(t *testing.T) {
		if !strings.Contains(workflow, "push: <true — only when --push was passed>") {
			t.Error("Step 5 does not pass push to commit_apply for --push")
		}
		if !strings.Contains(workflow, "**Push warnings:**") {
			t.Error("Step 6 does not print the push warnings")
		}
		if !strings.Contains(workflow, "push skipped: main is the default branch") {
			t.Error("Step 6 does not show the default-branch push warning")
		}
	})

	t.Run("flag pairs are independent", func(t *testing.T) {
		for _, want := range []string{
			"They are independent of each other and of `--auto`",
			"the second flag is a no-op, and the push still skips the default branch with a warning",
		} {
			if !strings.Contains(content, want) {
				t.Errorf("flag pair rule missing: %q", want)
			}
		}
	})

	t.Run("Step 7 after Step 6 with summary line", func(t *testing.T) {
		step6 := strings.Index(workflow, "### Step 6")
		step7 := strings.Index(workflow, "### Step 7")
		if step6 < 0 || step7 < 0 || step7 < step6 {
			t.Fatalf("Step 6=%d Step 7=%d: Step 7 must follow Step 6", step6, step7)
		}
		for _, want := range []string{
			"plugins/sdlc/skills/ship/instructions-run.md",
			"Loop without run state",
			"Instructions (commit): N total, X done, Y failed, Z not run.",
			"Run the remaining instructions by hand. A new run of the skill runs the whole list again unless you pass --no-instructions.",
		} {
			if !strings.Contains(workflow[step7:], want) {
				t.Errorf("Step 7 is missing %q", want)
			}
		}
	})

	t.Run("exit routes", func(t *testing.T) {
		step7 := workflow[strings.Index(workflow, "### Step 7"):]
		if !strings.Contains(step7, "A cancel at Step 5 or a tool error in") {
			t.Error("Step 7 does not name the routes that run no list")
		}
		if !strings.Contains(content, "the `Instructions (commit): …` line names each failed or skipped instruction") {
			t.Error("What's Next has no line for the summary")
		}
	})

	t.Run("absolute rules unchanged", func(t *testing.T) {
		if !strings.Contains(content, "never stages untracked files, never stashes, never amends") {
			t.Error("the commit absolute rule changed")
		}
	})
}
