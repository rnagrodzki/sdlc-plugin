package hooks

import (
	"strings"
	"time"

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

	return silent, nil
}
