package hooks

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
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
// unlike pipeline-continue's advisory nudges, this hook never injects
// additionalContext into an sdlc MCP call.
//
// It has one more write: after a plan_prepare call it stamps ctx.SessionID
// into the state of the plan run that the tool response names
// (data.sessionId) through stampPlanSession, so the dashboard can attach
// waits to the session that drives the run. The stamp never changes the hook
// result.
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
		stampPlanSession(root, ctx.SessionID, event.Raw["tool_response"])
	}

	return silent, nil
}

// planRunIDRe finds the run ID in the plan_prepare result: the Markdown
// form "runId: plan-..." and the JSON form "runId":"plan-...". The run ID
// grammar is state.PlanRunIDPattern.
var planRunIDRe = regexp.MustCompile(`runId"?:\s*"?(` + state.PlanRunIDPattern + `)`)

// claimPlanSession is state.ClaimSession; tests replace it to simulate a
// failed state write.
var claimPlanSession = state.ClaimSession

// stampPlanSession writes sessionID into the plan run that toolResponse
// names (runId). It writes nothing and prints nothing when:
//   - json.Marshal rejects toolResponse;
//   - toolResponse holds no run ID in the form of planRunIDRe;
//   - the named run has no file, is not active, or already has sessionID.
//
// A LoadRun error (for example bad JSON in the run file) prints one stderr
// line and writes nothing. A write error prints one stderr line. No error
// changes the hook result.
func stampPlanSession(root, sessionID string, toolResponse any) {
	raw, err := json.Marshal(toolResponse)
	if err != nil {
		return
	}
	m := planRunIDRe.FindSubmatch(raw)
	if m == nil {
		return
	}
	st, err := state.LoadRun(root, string(m[1]))
	if err != nil {
		fmt.Fprintf(os.Stderr, "[sdlc/hook record-mcp-invocation] plan session stamp: %v\n", err)
		return
	}
	if st == nil || !state.IsActivePlanRun(st) {
		return // no file, a done run, or no skillInvoked: no stamp
	}
	if current, _ := st.Data["sessionId"].(string); current == sessionID {
		return
	}
	if err := claimPlanSession(st, sessionID); err != nil {
		fmt.Fprintf(os.Stderr, "[sdlc/hook record-mcp-invocation] plan session stamp: %v\n", err)
	}
}
