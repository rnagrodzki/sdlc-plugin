package tools

import (
	"fmt"

	version "github.com/rnagrodzki/sdlc-plugin"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
)

// unknownActionError builds the DomainError a tool dispatcher returns when the
// caller names an action the running binary does not implement.
//
// The usual cause is version skew, not a typo: a skill newer than the
// installed MCP server calls an action the server predates. So the message
// names the binary's plugin version and build commit (the same values the
// session-start header prints), and the suggestion offers updating the plugin
// next to the site's own "pass a valid action" advice.
//
// Parameters:
//   - kind: what the tool calls the rejected value, e.g. "action" or
//     "jira action". The message reads `unknown <kind> "<action>"`.
//   - action: the rejected value, quoted with %q.
//   - msgTail: text appended to the message after the version suffix, such as
//     "; must be one of: a, b". Pass "" when the site has none.
//   - advice: the site's valid-action advice as a lowercase phrase with no
//     trailing period. It is spliced into "Either <advice>, or update ...".
func unknownActionError(kind, action, msgTail, advice string) *mcpserver.DomainError {
	info := version.GetBuildInfo()
	return &mcpserver.DomainError{
		Msg: fmt.Sprintf("unknown %s %q (sdlc v%s, commit %s)%s", kind, action, info.PluginVersion, info.Commit, msgTail),
		Suggestion: fmt.Sprintf("This action is not in the running binary. Either %s, or update the sdlc plugin — "+
			"the skill invoking this action may be newer than the installed server.", advice),
	}
}
