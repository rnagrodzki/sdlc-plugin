package hooks

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
	"github.com/rnagrodzki/sdlc-plugin/internal/state"
)

// ---------------------------------------------------------------------------
// Shared test helpers
//
// gitFixture, chdir, realPath, mustMkdirAll, mustWriteFile are defined in
// session_start_test.go (same package) and reused here.
// ---------------------------------------------------------------------------

// newShipState creates and writes a ship state file for branch with the
// given sessionID, steps, and flags (steps/flags nil means "leave the key
// unset entirely" — e.g. to exercise the absent-steps/absent-flags cases).
func newShipState(t *testing.T, root, branch, sessionID string, steps []any, flags map[string]any) {
	t.Helper()
	st, err := state.Init(root, "ship", branch, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if steps != nil {
		st.Data["steps"] = steps
	}
	if flags != nil {
		st.Data["flags"] = flags
	}
	if err := state.Write(st); err != nil {
		t.Fatal(err)
	}
}

func assertSilent(t *testing.T, out Output) {
	t.Helper()
	if out.JSON != nil {
		t.Errorf("JSON = %v, want nil (silent)", out.JSON)
	}
	if out.PlainText != "" {
		t.Errorf("PlainText = %q, want empty (silent)", out.PlainText)
	}
	if out.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", out.ExitCode)
	}
}

// ---------------------------------------------------------------------------
// block-askuserquestion-auto: deny-gate truth table
// ---------------------------------------------------------------------------

func TestBlockAskUserQuestionAuto(t *testing.T) {
	inProgressSteps := []any{
		map[string]any{"name": "review", "status": "in_progress"},
		map[string]any{"name": "pr", "status": "pending"},
	}
	allCompletedSteps := []any{
		map[string]any{"name": "review", "status": "completed"},
	}
	failedNoCleanupSteps := []any{
		map[string]any{"name": "review", "status": "failed"},
		map[string]any{"name": "pr", "status": "pending"},
	}

	t.Run("branch does not resolve (not a git repo): silent", func(t *testing.T) {
		chdir(t, realPath(t, t.TempDir()))
		out, err := blockAskUserQuestionAuto(HookCtx{SessionID: "s1"}, Event{})
		if err != nil {
			t.Fatal(err)
		}
		assertSilent(t, out)
	})

	t.Run("no state file: silent", func(t *testing.T) {
		gitFixture(t, "feat/no-state")
		out, err := blockAskUserQuestionAuto(HookCtx{SessionID: "s1"}, Event{})
		if err != nil {
			t.Fatal(err)
		}
		assertSilent(t, out)
	})

	t.Run("state has no steps key at all (non-array/absent): silent", func(t *testing.T) {
		root := gitFixture(t, "feat/absent-steps")
		newShipState(t, root, "feat/absent-steps", "s1", nil, map[string]any{"auto": true})
		out, err := blockAskUserQuestionAuto(HookCtx{SessionID: "s1"}, Event{})
		if err != nil {
			t.Fatal(err)
		}
		assertSilent(t, out)
	})

	t.Run("advancing=false, all steps terminal: silent", func(t *testing.T) {
		root := gitFixture(t, "feat/all-terminal")
		newShipState(t, root, "feat/all-terminal", "s1", allCompletedSteps, map[string]any{"auto": true})
		out, err := blockAskUserQuestionAuto(HookCtx{SessionID: "s1"}, Event{})
		if err != nil {
			t.Fatal(err)
		}
		assertSilent(t, out)
	})

	t.Run("advancing=false, failed without cleanup pending: silent", func(t *testing.T) {
		root := gitFixture(t, "feat/failed-no-cleanup")
		newShipState(t, root, "feat/failed-no-cleanup", "s1", failedNoCleanupSteps, map[string]any{"auto": true})
		out, err := blockAskUserQuestionAuto(HookCtx{SessionID: "s1"}, Event{})
		if err != nil {
			t.Fatal(err)
		}
		assertSilent(t, out)
	})

	t.Run("advancing=true, session mismatch: silent", func(t *testing.T) {
		root := gitFixture(t, "feat/session-mismatch")
		newShipState(t, root, "feat/session-mismatch", "state-session", inProgressSteps, map[string]any{"auto": true})
		out, err := blockAskUserQuestionAuto(HookCtx{SessionID: "different-session"}, Event{})
		if err != nil {
			t.Fatal(err)
		}
		assertSilent(t, out)
	})

	t.Run("advancing=true, session match, flags absent: silent", func(t *testing.T) {
		root := gitFixture(t, "feat/flags-absent")
		newShipState(t, root, "feat/flags-absent", "s1", inProgressSteps, nil)
		out, err := blockAskUserQuestionAuto(HookCtx{SessionID: "s1"}, Event{})
		if err != nil {
			t.Fatal(err)
		}
		assertSilent(t, out)
	})

	t.Run("advancing=true, session match, flags.auto absent: silent", func(t *testing.T) {
		root := gitFixture(t, "feat/auto-absent")
		newShipState(t, root, "feat/auto-absent", "s1", inProgressSteps, map[string]any{})
		out, err := blockAskUserQuestionAuto(HookCtx{SessionID: "s1"}, Event{})
		if err != nil {
			t.Fatal(err)
		}
		assertSilent(t, out)
	})

	t.Run("advancing=true, session match, flags.auto=false: silent", func(t *testing.T) {
		root := gitFixture(t, "feat/auto-false")
		newShipState(t, root, "feat/auto-false", "s1", inProgressSteps, map[string]any{"auto": false})
		out, err := blockAskUserQuestionAuto(HookCtx{SessionID: "s1"}, Event{})
		if err != nil {
			t.Fatal(err)
		}
		assertSilent(t, out)
	})

	t.Run("advancing=true, session match, flags.auto truthy non-boolean: silent (proves strict ===true check)", func(t *testing.T) {
		root := gitFixture(t, "feat/auto-truthy-string")
		newShipState(t, root, "feat/auto-truthy-string", "s1", inProgressSteps, map[string]any{"auto": "yes"})
		out, err := blockAskUserQuestionAuto(HookCtx{SessionID: "s1"}, Event{})
		if err != nil {
			t.Fatal(err)
		}
		assertSilent(t, out)
	})

	t.Run("all four conditions true: deny", func(t *testing.T) {
		root := gitFixture(t, "feat/deny")
		newShipState(t, root, "feat/deny", "s1", inProgressSteps, map[string]any{"auto": true})
		out, err := blockAskUserQuestionAuto(HookCtx{SessionID: "s1"}, Event{})
		if err != nil {
			t.Fatal(err)
		}
		if out.ExitCode != 0 {
			t.Errorf("ExitCode = %d, want 0 (deny is conveyed via JSON, not exit code)", out.ExitCode)
		}
		payload, ok := out.JSON.(map[string]any)
		if !ok {
			t.Fatalf("JSON = %v (%T), want map[string]any", out.JSON, out.JSON)
		}
		hso, ok := payload["hookSpecificOutput"].(map[string]any)
		if !ok {
			t.Fatalf("hookSpecificOutput missing or wrong type: %v", payload)
		}
		if hso["hookEventName"] != "PreToolUse" {
			t.Errorf("hookEventName = %v, want PreToolUse", hso["hookEventName"])
		}
		if hso["permissionDecision"] != "deny" {
			t.Errorf("permissionDecision = %v, want deny", hso["permissionDecision"])
		}
		if reason, _ := hso["permissionDecisionReason"].(string); reason == "" {
			t.Error("permissionDecisionReason is empty, want the documented deny message")
		}
	})
}

// ---------------------------------------------------------------------------
// pipeline-continue: 3-way branch
// ---------------------------------------------------------------------------

func mustAdditionalContext(t *testing.T, out Output, err error, wantSubstring string) {
	t.Helper()
	if err != nil {
		t.Fatalf("handler returned error: %v", err)
	}
	if out.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", out.ExitCode)
	}
	payload, ok := out.JSON.(map[string]any)
	if !ok {
		t.Fatalf("JSON = %v (%T), want map[string]any", out.JSON, out.JSON)
	}
	hso, ok := payload["hookSpecificOutput"].(map[string]any)
	if !ok {
		t.Fatalf("hookSpecificOutput missing or wrong type: %v", payload)
	}
	if hso["hookEventName"] != "PostToolUse" {
		t.Errorf("hookEventName = %v, want PostToolUse", hso["hookEventName"])
	}
	ac, _ := hso["additionalContext"].(string)
	if !strings.Contains(ac, wantSubstring) {
		t.Errorf("additionalContext = %q, want substring %q", ac, wantSubstring)
	}
}

func TestPipelineContinue(t *testing.T) {
	inProgressSteps := []any{
		map[string]any{"name": "review", "status": "in_progress"},
		map[string]any{"name": "pr", "status": "pending"},
	}
	pendingSteps := []any{
		map[string]any{"name": "review", "status": "completed"},
		map[string]any{"name": "pr", "status": "pending"},
	}
	allCompletedSteps := []any{
		map[string]any{"name": "review", "status": "completed"},
	}

	t.Run("in_progress fires continue-context with auto=true", func(t *testing.T) {
		root := gitFixture(t, "feat/pc-inprog-true")
		newShipState(t, root, "feat/pc-inprog-true", "s1", inProgressSteps, map[string]any{"auto": true})
		out, err := pipelineContinue(HookCtx{SessionID: "s1"}, Event{})
		mustAdditionalContext(t, out, err, "step 1 of 2 (review) is in_progress")
	})

	t.Run("in_progress fires continue-context with auto=false (mode-independent)", func(t *testing.T) {
		root := gitFixture(t, "feat/pc-inprog-false")
		newShipState(t, root, "feat/pc-inprog-false", "s1", inProgressSteps, map[string]any{"auto": false})
		out, err := pipelineContinue(HookCtx{SessionID: "s1"}, Event{})
		mustAdditionalContext(t, out, err, "step 1 of 2 (review) is in_progress")
	})

	t.Run("pending fires advance-context when auto=true", func(t *testing.T) {
		root := gitFixture(t, "feat/pc-pending-true")
		newShipState(t, root, "feat/pc-pending-true", "s1", pendingSteps, map[string]any{"auto": true})
		out, err := pipelineContinue(HookCtx{SessionID: "s1"}, Event{})
		mustAdditionalContext(t, out, err, "step 2 of 2 (pr), pending")
	})

	t.Run("pending silent when auto=false", func(t *testing.T) {
		root := gitFixture(t, "feat/pc-pending-false")
		newShipState(t, root, "feat/pc-pending-false", "s1", pendingSteps, map[string]any{"auto": false})
		out, err := pipelineContinue(HookCtx{SessionID: "s1"}, Event{})
		if err != nil {
			t.Fatal(err)
		}
		assertSilent(t, out)
	})

	t.Run("pending fires advance-context on truthy non-boolean auto (loose check, differs from strict)", func(t *testing.T) {
		root := gitFixture(t, "feat/pc-pending-loose")
		newShipState(t, root, "feat/pc-pending-loose", "s1", pendingSteps, map[string]any{"auto": "yes"})

		out, err := pipelineContinue(HookCtx{SessionID: "s1"}, Event{})
		mustAdditionalContext(t, out, err, "step 2 of 2 (pr), pending")

		// Same fixture family, same truthy-non-boolean auto value: proves
		// block-askuserquestion-auto's strict check does NOT deny here,
		// showing the two hooks' auto-checks are independently implemented.
		denyOut, denyErr := blockAskUserQuestionAuto(HookCtx{SessionID: "s1"}, Event{})
		if denyErr != nil {
			t.Fatal(denyErr)
		}
		assertSilent(t, denyOut)
	})

	t.Run("not advancing: silent", func(t *testing.T) {
		root := gitFixture(t, "feat/pc-not-advancing")
		newShipState(t, root, "feat/pc-not-advancing", "s1", allCompletedSteps, map[string]any{"auto": true})
		out, err := pipelineContinue(HookCtx{SessionID: "s1"}, Event{})
		if err != nil {
			t.Fatal(err)
		}
		assertSilent(t, out)
	})

	t.Run("session mismatch: silent", func(t *testing.T) {
		root := gitFixture(t, "feat/pc-session-mismatch")
		newShipState(t, root, "feat/pc-session-mismatch", "state-session", inProgressSteps, map[string]any{"auto": true})
		out, err := pipelineContinue(HookCtx{SessionID: "different"}, Event{})
		if err != nil {
			t.Fatal(err)
		}
		assertSilent(t, out)
	})
}

func TestStepDisplayName(t *testing.T) {
	cases := []struct {
		name string
		step map[string]any
		want string
	}{
		{"name present", map[string]any{"name": "review", "id": "step-1"}, "review"},
		{"name empty, falls back to id", map[string]any{"name": "", "id": "step-1"}, "step-1"},
		{"neither present", map[string]any{}, "unknown"},
		{"nil step", nil, "unknown"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := stepDisplayName(c.step); got != c.want {
				t.Errorf("stepDisplayName(%v) = %q, want %q", c.step, got, c.want)
			}
		})
	}
}

func TestJsTruthyAutoFlag(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want bool
	}{
		{"nil", nil, false},
		{"true", true, true},
		{"false", false, false},
		{"empty string", "", false},
		{"non-empty string", "yes", true},
		{"zero int", 0, false},
		{"nonzero int", 1, true},
		{"zero float64", float64(0), false},
		{"nonzero float64", float64(1), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := jsTruthyAutoFlag(c.in); got != c.want {
				t.Errorf("jsTruthyAutoFlag(%v) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// post-tool-validate: path regexes + in-process validator dispatch
// ---------------------------------------------------------------------------

func TestPostToolValidateRegexes(t *testing.T) {
	cases := []struct {
		name string
		re   *regexp.Regexp
		path string
		want bool
	}{
		{"dimension canonical .sdlc", postToolValidateDimensionRe, "/repo/.sdlc/review-dimensions/security.yaml", true},
		{"dimension legacy .claude", postToolValidateDimensionRe, "/repo/.claude/review-dimensions/security.yml", true},
		{"dimension wrong dir", postToolValidateDimensionRe, "/repo/.sdlc/other-dir/security.yaml", false},
		{"dimension wrong extension", postToolValidateDimensionRe, "/repo/.sdlc/review-dimensions/security.md", false},

		{"pr-template canonical .sdlc", postToolValidatePRTemplateRe, "/repo/.sdlc/pr-template.md", true},
		{"pr-template legacy .claude", postToolValidatePRTemplateRe, "/repo/.claude/pr-template.md", true},
		{"pr-template wrong name", postToolValidatePRTemplateRe, "/repo/.sdlc/pr-template.txt", false},

		{"plan match", postToolValidatePlanRe, "/repo/plans/my-plan.md", true},
		{"plan wrong dir", postToolValidatePlanRe, "/repo/other/my-plan.md", false},
		{"plan wrong extension", postToolValidatePlanRe, "/repo/plans/my-plan.txt", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.re.MatchString(c.path); got != c.want {
				t.Errorf("MatchString(%q) = %v, want %v", c.path, got, c.want)
			}
		})
	}
}

func triggerEvent(filePath string) Event {
	return Event{Raw: map[string]any{"tool_input": map[string]any{"file_path": filePath}}}
}

func mustBlock(t *testing.T, out Output, err error, wantSubstring string) {
	t.Helper()
	if err != nil {
		t.Fatalf("handler returned error: %v", err)
	}
	if out.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0 (blocking is conveyed via JSON, not exit code — Flag 1 disclosed deviation)", out.ExitCode)
	}
	payload, ok := out.JSON.(map[string]any)
	if !ok {
		t.Fatalf("JSON = %v (%T), want map[string]any", out.JSON, out.JSON)
	}
	if payload["decision"] != "block" {
		t.Errorf("decision = %v, want block", payload["decision"])
	}
	reason, _ := payload["reason"].(string)
	if !strings.Contains(reason, wantSubstring) {
		t.Errorf("reason = %q, want substring %q", reason, wantSubstring)
	}
}

func TestPostToolValidate_NoMatch_Silent(t *testing.T) {
	dir := realPath(t, t.TempDir())
	chdir(t, dir)

	out, err := postToolValidate(HookCtx{}, triggerEvent(filepath.Join(dir, "README.md")))
	if err != nil {
		t.Fatal(err)
	}
	assertSilent(t, out)
}

func TestPostToolValidate_NoFileArgAtAll_Silent(t *testing.T) {
	dir := realPath(t, t.TempDir())
	chdir(t, dir)

	out, err := postToolValidate(HookCtx{}, Event{Raw: map[string]any{"tool_input": map[string]any{}}})
	if err != nil {
		t.Fatal(err)
	}
	assertSilent(t, out)
}

func TestPostToolValidate_Dimensions_ZeroFindings_Silent(t *testing.T) {
	dir := realPath(t, t.TempDir())
	chdir(t, dir)

	// No .sdlc/review-dimensions directory at all: dimensions.Load yields
	// zero dimensions, so validateDimensionsAction produces zero findings.
	out, err := postToolValidate(HookCtx{}, triggerEvent(filepath.Join(dir, paths.DataDir, "review-dimensions", "security.yaml")))
	if err != nil {
		t.Fatal(err)
	}
	assertSilent(t, out)
}

func TestPostToolValidate_Dimensions_Findings_Blocks(t *testing.T) {
	dir := realPath(t, t.TempDir())
	chdir(t, dir)

	dimDir := filepath.Join(dir, paths.DataDir, "review-dimensions")
	mustMkdirAll(t, dimDir)
	mustWriteFile(t, filepath.Join(dimDir, "bad.md"), "no frontmatter delimiters in this file\n")

	// Trigger via the legacy .claude/ alternation: the validator always
	// scans .sdlc/review-dimensions regardless of which alternation matched
	// the edited path (no file argument is passed either way).
	out, err := postToolValidate(HookCtx{}, triggerEvent(filepath.Join(dir, ".claude", "review-dimensions", "security.yaml")))
	mustBlock(t, out, err, "D1")
}

func TestPostToolValidate_PRTemplate_MissingFile_Blocks(t *testing.T) {
	dir := realPath(t, t.TempDir())
	chdir(t, dir)

	// No pr-template.md at either canonical or legacy location:
	// validatePRTemplate produces a V1 "File not found" finding.
	out, err := postToolValidate(HookCtx{}, triggerEvent(filepath.Join(dir, paths.DataDir, "pr-template.md")))
	mustBlock(t, out, err, "V1")
}

const validPRTemplateFixture = `## Summary

This section explains what changed and why, in enough detail to satisfy the minimum body length check.

## Testing

This section explains how the change was tested, again with enough content to clear the minimum length threshold.
`

func TestPostToolValidate_PRTemplate_ZeroFindings_Silent(t *testing.T) {
	dir := realPath(t, t.TempDir())
	chdir(t, dir)

	mustMkdirAll(t, filepath.Join(dir, paths.DataDir))
	mustWriteFile(t, filepath.Join(dir, paths.DataDir, "pr-template.md"), validPRTemplateFixture)

	// Trigger via the legacy .claude/ alternation even though the canonical
	// .sdlc/pr-template.md is what actually gets resolved and validated
	// (validatePRTemplate resolves the template itself; no file arg passed).
	out, err := postToolValidate(HookCtx{}, triggerEvent(filepath.Join(dir, ".claude", "pr-template.md")))
	if err != nil {
		t.Fatal(err)
	}
	assertSilent(t, out)
}

// validPlanFixtureMissingScorecard satisfies PF1-PF7 but deliberately omits
// the "## Verification Scorecard" section PF9 requires. If postToolValidate
// ever passed Final: true (it must always pass false), this fixture would
// flip from silent to blocking.
const validPlanFixtureMissingScorecard = `**Goal:** Do the thing
**Architecture:** Some arch
**Source:** origin
**Verification:** tests

### Task 1: Implement feature

**Complexity:** Trivial
**Risk:** Low
**Depends on:** none
**Verify:** tests

**Acceptance criteria:**
- [ ] does the thing

## Deviations & assumptions

None.
`

func TestPostToolValidate_Plan_FinalAlwaysFalse(t *testing.T) {
	dir := realPath(t, t.TempDir())
	chdir(t, dir)

	plansDir := filepath.Join(dir, "plans")
	mustMkdirAll(t, plansDir)
	planPath := filepath.Join(plansDir, "my-plan.md")
	mustWriteFile(t, planPath, validPlanFixtureMissingScorecard)

	out, err := postToolValidate(HookCtx{}, triggerEvent(planPath))
	if err != nil {
		t.Fatal(err)
	}
	assertSilent(t, out)
}

func TestPostToolValidate_Plan_Findings_Blocks(t *testing.T) {
	dir := realPath(t, t.TempDir())
	chdir(t, dir)

	plansDir := filepath.Join(dir, "plans")
	mustMkdirAll(t, plansDir)
	planPath := filepath.Join(plansDir, "empty-plan.md")
	mustWriteFile(t, planPath, "nothing here\n")

	out, err := postToolValidate(HookCtx{}, triggerEvent(planPath))
	mustBlock(t, out, err, "PF1")
}

func TestPostToolValidate_PathFallback_WhenFilePathAbsent(t *testing.T) {
	dir := realPath(t, t.TempDir())
	chdir(t, dir)

	plansDir := filepath.Join(dir, "plans")
	mustMkdirAll(t, plansDir)
	planPath := filepath.Join(plansDir, "my-plan.md")
	mustWriteFile(t, planPath, validPlanFixtureMissingScorecard)

	event := Event{Raw: map[string]any{"tool_input": map[string]any{"path": planPath}}}
	out, err := postToolValidate(HookCtx{}, event)
	if err != nil {
		t.Fatal(err)
	}
	assertSilent(t, out)
}

func TestPostToolValidate_FilePathTakesPriorityOverPath(t *testing.T) {
	dir := realPath(t, t.TempDir())
	chdir(t, dir)

	plansDir := filepath.Join(dir, "plans")
	mustMkdirAll(t, plansDir)
	planPath := filepath.Join(plansDir, "my-plan.md")
	mustWriteFile(t, planPath, validPlanFixtureMissingScorecard)

	// file_path is a non-matching path; path is a matching one. Priority
	// order (tool_input.file_path || tool_input.path) means file_path wins,
	// so this must stay silent, not block on the plan file named in "path".
	event := Event{Raw: map[string]any{"tool_input": map[string]any{
		"file_path": filepath.Join(dir, "README.md"),
		"path":      planPath,
	}}}
	out, err := postToolValidate(HookCtx{}, event)
	if err != nil {
		t.Fatal(err)
	}
	assertSilent(t, out)
}

// TestPostToolValidate_NoSubprocess is the AC's explicit "grep the diff for
// exec.Command/os/exec" check, enforced as a real assertion against the
// handler's own source file rather than left as a manual review step.
func TestPostToolValidate_NoSubprocess(t *testing.T) {
	src, err := os.ReadFile("post_tool_validate.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(src)
	for _, banned := range []string{"os/exec", "exec.Command"} {
		if strings.Contains(text, banned) {
			t.Errorf("post_tool_validate.go contains %q; this hook must run validators in-process, not via subprocess", banned)
		}
	}
}
