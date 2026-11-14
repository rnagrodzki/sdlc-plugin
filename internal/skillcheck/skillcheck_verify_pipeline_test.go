// Package skillcheck pins the standalone instructions steps of
// plugins/sdlc/skills/verify-pipeline/SKILL.md: the --no-instructions flag,
// the resolve call after Step 1, the list after the Step 4 JSON verdict line
// and the routes that run no list. Every unexported identifier below is
// prefixed verifyPipelineSkill.
package skillcheck

import (
	"path/filepath"
	"strings"
	"testing"
)

// verifyPipelineSkillRead returns the text of verify-pipeline/SKILL.md.
func verifyPipelineSkillRead(t *testing.T) string {
	t.Helper()
	return executeSkillsReadFile(t, filepath.Join(executeSkillsRepoRoot(t), "skills", "verify-pipeline", "SKILL.md"))
}

func TestVerifyPipelineSkillFlags(t *testing.T) {
	skill := verifyPipelineSkillRead(t)

	t.Run("argument hint", func(t *testing.T) {
		const want = "[--pr <number>] [--logs <path-or-string>] [--auto] [--no-instructions]"
		if got := flagParityFrontmatterField(skill, "argument-hint"); got != want {
			t.Errorf("argument-hint = %q, want %q", got, want)
		}
		if !flagParityFlagSet(flagParityFrontmatterField(skill, "description"))["--no-instructions"] {
			t.Error("description does not name --no-instructions")
		}
	})

	t.Run("tools resolve", func(t *testing.T) {
		flagParityAssertToolsResolve(t, "verify-pipeline", "instructions_resolve", "verify_pipeline_classify")
	})

	t.Run("instructions text", func(t *testing.T) {
		for _, want := range []string{
			"## Step 1b: Resolve instructions",
			`instructions_resolve({ key: "verify-pipeline" })`,
			"stop before Step 2. Print no JSON verdict line.",
			"## Step 5: Run instructions",
			"Never run it before the JSON line.",
			"The run mode is always interactive: `--auto` does not change it.",
			"Instructions (verify-pipeline): N total, X done, Y failed, Z not run.",
			"Run the remaining instructions by hand.",
			"except an instruction from config (Step 5, standalone run only)",
			"| resolve error (Step 1b) |",
			"| `abort` verdict (Step 1 or Step 4) | no |",
			"| `fix-applied` or `proposal` verdict, no `--no-instructions` | yes, after the JSON line |",
			"| Step 5 interrupted |",
			"In a standalone run, the `Instructions (verify-pipeline): …` line follows the JSON line.",
			"The commit does not push, because ship does not pass `--push` on this commit, so the manual-push pause stays.",
		} {
			if !strings.Contains(skill, want) {
				t.Errorf("skills/verify-pipeline/SKILL.md: missing %q", want)
			}
		}
	})

	t.Run("heading order", func(t *testing.T) {
		order := []string{
			"## Step 1: CONSUME",
			"## Step 1b: Resolve instructions",
			"## Step 2: CLASSIFY",
			"## Step 4: VERDICT",
			"## Step 5: Run instructions",
			"## What's Next",
		}
		last := -1
		for _, h := range order {
			i := strings.Index(skill, h)
			if i < 0 {
				t.Errorf("skills/verify-pipeline/SKILL.md: missing heading %q", h)
				continue
			}
			if i < last {
				t.Errorf("skills/verify-pipeline/SKILL.md: heading %q is out of order", h)
			}
			last = i
		}
	})

	// The ship parser reads one JSON line. The Step 5 example must show the
	// summary after that line, never before it.
	t.Run("summary after the JSON line", func(t *testing.T) {
		_, step5, ok := strings.Cut(skill, "## Step 5: Run instructions")
		if !ok {
			t.Fatal("skills/verify-pipeline/SKILL.md: no Step 5")
		}
		step5, _, _ = strings.Cut(step5, "\n## ")
		jsonLine := strings.Index(step5, `{"status":"fix-applied"`)
		summary := strings.Index(step5, "Instructions (verify-pipeline):")
		if jsonLine < 0 || summary < 0 {
			t.Fatalf("Step 5 example lacks the JSON line (%d) or the summary line (%d)", jsonLine, summary)
		}
		if summary < jsonLine {
			t.Error("Step 5 example prints the summary line before the JSON verdict line")
		}
	})
}
