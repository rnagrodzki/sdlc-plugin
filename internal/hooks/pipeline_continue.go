package hooks

import (
	"fmt"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/tools"
)

// pipelineContinue is the "pipeline-continue" hook handler (PostToolUse,
// matcher Bash|TodoWrite), ported from hooks/pipeline-continue.js. Unlike
// block-askuserquestion-auto's binary deny gate, this is a 3-way branch: it
// nudges Claude to keep working an active ship pipeline via
// additionalContext, or stays silent.
//
// Shares the same root/branch resolution and PipelineAdvancing/
// HookEnforcementAllowed gate as block-askuserquestion-auto
// (gatedAdvancingShipState, in block_askuserquestion.go) — never
// re-implemented here.
func pipelineContinue(ctx HookCtx, event Event) (Output, error) {
	silent := Output{ExitCode: 0}

	// Auto-record this Bash execution as CLI evidence (Task 10), superseding
	// the explicit execute_state/ship_state {action:"log-cli"} calls
	// SKILL.md previously instructed. Runs unconditionally, before the
	// advancing-ship-state gate below: recording is passive and must happen
	// regardless of whether a ship pipeline is actively advancing or this
	// session owns it (TodoWrite calls are filtered out inside the helper).
	recordBashExecution(event)

	data, adv, ok := gatedAdvancingShipState("pipeline-continue", ctx.SessionID)
	if !ok {
		return silent, nil
	}

	total := 0
	if steps, isArr := data["steps"].([]any); isArr {
		total = len(steps)
	}
	name := stepDisplayName(adv.Step)
	status, _ := adv.Step["status"].(string)

	if status == "in_progress" {
		// Mode-independent: fires regardless of flags.auto.
		msg := fmt.Sprintf(
			"Ship pipeline: step %d of %d (%s) is in_progress. Continue executing this step — do not end the response turn. Next action: record the step result and advance to the next step.",
			adv.Index+1, total, name,
		)
		return pipelineContinueOutput(msg), nil
	}

	// Step is the first non-terminal pending step (not in_progress): only
	// nudge to advance in --auto mode. This is deliberately the LOOSE
	// truthy check (flags.auto merely truthy), not the strict ===true check
	// block-askuserquestion-auto.go uses — a real, source-intentional
	// divergence, not something to normalize away.
	flags, _ := data["flags"].(map[string]any)
	if !jsTruthyAutoFlag(flags["auto"]) {
		return silent, nil
	}

	msg := fmt.Sprintf(
		"Ship pipeline: the next step is step %d of %d (%s), pending. In --auto mode, advance to it now — do not end the response turn. Next action: begin the next step.",
		adv.Index+1, total, name,
	)
	return pipelineContinueOutput(msg), nil
}

func pipelineContinueOutput(additionalContext string) Output {
	return Output{JSON: map[string]any{
		"hookSpecificOutput": map[string]any{
			"hookEventName":     "PostToolUse",
			"additionalContext": additionalContext,
		},
	}, ExitCode: 0}
}

// stepDisplayName mirrors source line 90's step.name || step.id || "unknown"
// fallback chain.
func stepDisplayName(step map[string]any) string {
	if name, ok := step["name"].(string); ok && name != "" {
		return name
	}
	if id, ok := step["id"].(string); ok && id != "" {
		return id
	}
	return "unknown"
}

// jsTruthyAutoFlag mirrors JS truthiness for the small set of JSON-decoded
// scalar types flags.auto can take: nil -> false, bool -> itself,
// string -> non-empty, numeric -> non-zero. Objects/arrays (never a real
// flags.auto value in practice) fall through to true, matching plain JS
// truthiness. This is a deliberate 4th small unexported copy of this repo's
// existing per-package "JS truthiness" helpers (internal/discovery.jsTruthy,
// internal/dimensions.jsFalsy, internal/tools.jsFalsyLocal) rather than a
// cross-package import of any of them — see task fact sheet.
func jsTruthyAutoFlag(v any) bool {
	if v == nil {
		return false
	}
	switch x := v.(type) {
	case bool:
		return x
	case string:
		return x != ""
	case int:
		return x != 0
	case int64:
		return x != 0
	case float64:
		return x != 0
	}
	return true
}

// cliDedupWindow bounds how close in time two identical (branch, command,
// exitCode) CLI evidence entries must be recorded to be treated as the same
// physical Bash invocation rather than a deliberate re-run of the same
// command. Scoped to THIS hook's own write path only (not moved into
// tools.appendCLIEvidence): internal/tools/cli_evidence_test.go has
// pre-existing tests that call appendCLIEvidence directly in a tight loop
// with identical branch/command/exitCode and expect every call to land as
// its own line, so a central dedup guard there would silently drop
// legitimate repeated-identical-command sequences. This narrowly catches a
// genuine double-fire of this hook for the same tool call; it does NOT
// intercept a separate explicit execute_state/ship_state "log-cli" call —
// SKILL.md's removal of those explicit calls (execute/SKILL.md,
// ship/SKILL.md, this same task) is what eliminates that duplication path
// in practice. A caller that still invokes "log-cli" directly (bypassing
// SKILL.md, e.g. a stale in-flight session or manual MCP call) can still
// double-record; accepted as a residual, non-blocking gap — see the task's
// drift-log entry.
const cliDedupWindow = 2 * time.Second

// recordBashExecution auto-records a Bash tool's CLI execution to
// .sdlc-v2/evidence/cli-executions.jsonl (Task 10). Fire-and-forget: every
// bail-out path (not a Bash call, no repo root/branch, no command, no ship
// or execute state for this branch, a detected duplicate, a write error) is
// silently swallowed — this must never affect pipelineContinue's own return
// value or block the session.
func recordBashExecution(event Event) {
	if event.ToolName != "Bash" {
		return
	}

	toolInput, _ := event.Raw["tool_input"].(map[string]any)
	command, _ := toolInput["command"].(string)
	if command == "" {
		return
	}

	root, branch, ok := resolveRootBranch()
	if !ok {
		return
	}

	pipelineName, step, wave := resolvePipelineStepContext(root, branch)
	if pipelineName == "" {
		// No ship or execute state for this branch: nothing to attribute
		// the execution to. Mirrors saveCompactRecovery's "neither exists,
		// nothing to save" bail (pre_compact_save.go).
		return
	}

	entry := tools.CLIEvidenceEntry{
		Timestamp:  time.Now().UTC().Format(time.RFC3339),
		Pipeline:   pipelineName,
		Step:       step,
		Wave:       wave,
		Branch:     branch,
		Command:    command,
		ExitCode:   extractBashExitCode(event.ToolResponse),
		OutputHead: extractOutputHead(event.ToolResponse),
	}

	if isDuplicateCLIEvidence(root, entry) {
		return
	}

	_ = tools.AppendCLIEvidence(root, entry)
}

// isDuplicateCLIEvidence reports whether entry looks like an immediate
// re-fire of the most recently recorded CLI evidence entry for the SAME
// branch: same branch, same command, same exit code, timestamps within
// cliDedupWindow. Branch-scoped so two worktrees on different branches
// running the same command within the window are never conflated. This
// never suppresses a deliberate re-run of the same command more than
// cliDedupWindow apart — only a genuine double invocation of this hook for
// the same tool call.
func isDuplicateCLIEvidence(root string, entry tools.CLIEvidenceEntry) bool {
	last, ok, err := tools.LastCLIEvidenceEntry(root)
	if err != nil || !ok {
		return false
	}
	if last.Branch != entry.Branch || last.Command != entry.Command || last.ExitCode != entry.ExitCode {
		return false
	}

	lastTs, err1 := time.Parse(time.RFC3339, last.Timestamp)
	curTs, err2 := time.Parse(time.RFC3339, entry.Timestamp)
	if err1 != nil || err2 != nil {
		return false
	}

	diff := curTs.Sub(lastTs)
	if diff < 0 {
		diff = -diff
	}
	return diff <= cliDedupWindow
}

// extractBashExitCode best-effort reads a numeric exit code from a Bash
// PostToolUse tool_response. Claude Code's documented BashOutput shape
// (stdout, stderr, interrupted, ...) carries no such field on the normal
// PostToolUse success path — an "Exit code N" line only appears embedded in
// PostToolUseFailure's error string, a different hook event this handler
// does not receive — so this normally returns 0. It also checks
// exit_code/exitCode keys in case a future or wrapped tool_response ever
// supplies one explicitly.
func extractBashExitCode(toolResponse map[string]any) int {
	for _, key := range []string{"exit_code", "exitCode"} {
		if v, ok := toolResponse[key]; ok {
			return asInt(v)
		}
	}
	return 0
}

// extractOutputHead returns the first ~500 characters of a Bash
// tool_response's stdout, mirroring CLIEvidenceEntry.OutputHead's existing
// "first ~500 chars of output" contract (internal/tools/cli_evidence.go).
func extractOutputHead(toolResponse map[string]any) string {
	const maxLen = 500
	stdout, _ := toolResponse["stdout"].(string)
	if len(stdout) > maxLen {
		return stdout[:maxLen]
	}
	return stdout
}

// asInt converts a JSON-decoded numeric value (float64 in practice) to int.
// Returns 0 for nil or any non-numeric type.
func asInt(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	}
	return 0
}
