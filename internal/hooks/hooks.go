// Package hooks implements the "sdlc hook <name>" one-shot CLI subcommand:
// read the hook's stdin envelope, dispatch to a named Handler, and write its
// result to stdout. This mirrors the plugin's Node.js hooks/*.js scripts,
// which Claude Code invokes as one-shot processes wired up in hooks.json.
//
// Every handler follows a fail-open philosophy: a hook must never block or
// crash a Claude Code session. Run wraps handler dispatch in a panic-recovery
// net as a last-resort safety measure, but each handler is itself expected to
// degrade silently (partial or empty output) on internal errors rather than
// propagate them — matching the JS sources' per-phase try/catch structure.
package hooks

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
)

// PluginVersion is the sdlc plugin's own version string. cmd/sdlc's main()
// wires this from its own pluginVersion const before dispatching to Run
// (Task 40); it defaults to "unknown" so handlers behave sanely if invoked
// before that wiring exists (e.g. in tests, which set it explicitly).
var PluginVersion = "unknown"

// HookCtx carries per-invocation context threaded through to a Handler.
type HookCtx struct {
	// SessionID is Claude Code's session_id for this invocation, when the
	// stdin envelope carries one. Handlers that don't need it may ignore it.
	SessionID string
}

// Event is the parsed stdin envelope a hook receives on invocation. Raw
// holds the full decoded JSON object, or nil if stdin was empty, unreadable,
// or not a JSON object. Source is envelope.source (e.g. "startup", "resume",
// "clear", "compact"), defaulting to "startup" when absent or unparseable —
// mirroring session-start.js's own default.
type Event struct {
	Raw    map[string]any
	Source string
}

// Output is a Handler's result. PlainText is written to stdout verbatim.
// JSON, when non-nil, is encoded to stdout instead — the two are mutually
// exclusive in practice, but the shape supports either since the hook
// contract may call for one or the other depending on the hook. ExitCode is
// returned as Run's result (0 by default).
type Output struct {
	PlainText string
	JSON      any
	ExitCode  int
}

// Handler implements one named hook.
type Handler func(ctx HookCtx, event Event) (Output, error)

var registry = map[string]Handler{
	"session-start":              sessionStart,
	"block-askuserquestion-auto": blockAskUserQuestionAuto,
	"pipeline-continue":          pipelineContinue,
	"post-tool-validate":         postToolValidate,
	"pre-compact-save":           preCompactSave,
	"stop-state-save":            stopStateSave,
	"stop-plan-integrity":        stopPlanIntegrity,
	"stop-pipeline-continue":     stopPipelineContinue,
}

// Run reads the hook's stdin envelope, dispatches to the handler registered
// under name, and writes its output to stdout. It always returns an exit
// code and never panics out of the process: hooks are fail-open by
// contract, so an unknown hook name, a stdin read/parse failure, or a
// handler error/panic all degrade to a 0 exit with no stdout output, plus a
// diagnostic on stderr for debugging.
func Run(name string, stdin io.Reader, stdout io.Writer) int {
	ctx, event := readEvent(stdin)

	handler, ok := registry[name]
	if !ok {
		fmt.Fprintf(os.Stderr, "[sdlc/hook] unknown hook: %s\n", name)
		return 0
	}

	out, err := invokeSafely(handler, ctx, event)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[sdlc/hook %s] %v\n", name, err)
		return 0
	}

	writeOutput(stdout, out)
	return out.ExitCode
}

// readEvent best-effort reads and decodes stdin into an Event (and pulls a
// session_id, if present, into a HookCtx). Any failure — no stdin, empty
// stdin, invalid JSON, or a top-level JSON value that isn't an object —
// degrades to a zero-value Event with Source "startup", never an error.
func readEvent(stdin io.Reader) (HookCtx, Event) {
	event := Event{Source: "startup"}
	var ctx HookCtx

	if stdin == nil {
		return ctx, event
	}

	raw, err := io.ReadAll(stdin)
	if err != nil || len(raw) == 0 {
		return ctx, event
	}

	var envelope map[string]any
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return ctx, event
	}

	event.Raw = envelope
	if source, ok := envelope["source"].(string); ok && source != "" {
		event.Source = source
	}
	if sid, ok := envelope["session_id"].(string); ok {
		ctx.SessionID = sid
	}

	return ctx, event
}

// invokeSafely calls handler, converting any panic into an error so that Run
// can apply the same fail-open diagnostic-and-continue treatment uniformly.
func invokeSafely(handler Handler, ctx HookCtx, event Event) (out Output, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	return handler(ctx, event)
}

// writeOutput writes out's JSON payload if present, else its plain text, to
// stdout. Write failures are ignored: stdout is best-effort, matching the
// fail-open contract (a hook must never fail the session over an I/O error
// writing its own advisory output).
func writeOutput(stdout io.Writer, out Output) {
	if out.JSON != nil {
		enc := json.NewEncoder(stdout)
		_ = enc.Encode(out.JSON)
		return
	}
	if out.PlainText != "" {
		_, _ = io.WriteString(stdout, out.PlainText)
	}
}
