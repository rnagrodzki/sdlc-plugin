package hooks

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/state"
	"github.com/rnagrodzki/sdlc-plugin/internal/tools"
)

// mcpToolPrefix is the naming convention every sdlc MCP tool call carries in
// tool_name — see plugins/sdlc/.claude-plugin/plugin.json's mcpServers.sdlc
// wiring and docs/smoke-test.md.
const mcpToolPrefix = "mcp__plugin_sdlc_sdlc__"

// recordMCPInvocation is the "record-mcp-invocation" hook handler
// (PostToolUse, matcher mcp__plugin_sdlc_sdlc__.*), added for Task 10. It
// appends one evidence line per sdlc MCP tool call to
// .sdlc-v2/evidence/mcp-invocations.jsonl. Always returns a silent Output:
// unlike pipeline-continue's advisory nudges, this hook is passive recording
// only and must never inject additionalContext into every sdlc MCP call.
//
// Side effect: after a plan_prepare call it also stamps ctx.SessionID into the
// active plan run's state (data.sessionId) through stampPlanSession, so the
// dashboard can attach waits to the session that drives the run. The stamp
// never changes the hook result.
//
// No durationMs is recorded: a PostToolUse-only hook has no paired
// PreToolUse timestamp to measure invocation duration against, and pairing
// the two is explicitly out of this task's scope.
func recordMCPInvocation(ctx HookCtx, event Event) (Output, error) {
	silent := Output{ExitCode: 0}

	if !strings.HasPrefix(event.ToolName, mcpToolPrefix) {
		return silent, nil
	}

	root, branch, ok := resolveRootBranch()
	if !ok {
		return silent, nil
	}

	skillContext, _, _ := resolvePipelineStepContext(root, branch)

	entry := tools.MCPEvidenceEntry{
		Timestamp:    time.Now().UTC().Format(time.RFC3339),
		Tool:         event.ToolName,
		SessionID:    ctx.SessionID,
		SkillContext: skillContext,
	}

	// Fire-and-forget: a write failure must never affect this hook's return
	// value (always silent/ExitCode 0) or the calling tool's own result.
	_ = tools.AppendMCPEvidence(root, entry)

	if event.ToolName == mcpToolPrefix+"plan_prepare" && ctx.SessionID != "" {
		stampPlanSession(root, branch, ctx.SessionID)
	}

	return silent, nil
}

// stampPlanSession writes sessionID into the active plan run of branch.
// No active run, or the same ID: no write. An error: one stderr line, no
// effect on the hook result.
func stampPlanSession(root, branch, sessionID string) {
	st, err := state.ActivePlanRun(root, branch)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sdlc hook: plan session stamp: %v\n", err)
		return
	}
	if st == nil {
		return
	}
	if current, _ := st.Data["sessionId"].(string); current == sessionID {
		return
	}
	if err := state.ClaimSession(st, sessionID); err != nil {
		fmt.Fprintf(os.Stderr, "sdlc hook: plan session stamp: %v\n", err)
	}
}
