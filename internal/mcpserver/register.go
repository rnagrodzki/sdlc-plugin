package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Ctx carries per-call context passed to typed tool handlers.
type Ctx struct {
	SessionID string
	Quiet     bool // Reserved for future use; defaults to false.
	Warnings  *Dedup
}

// Annotations is the full MCP annotation set for a tool. Every registered
// tool declares one; there is no unannotated state.
//
// Destructive and Idempotent are meaningful only when ReadOnly is false
// (MCP spec). A ReadOnly tool may leave them at their zero values.
type Annotations struct {
	// Title is the short human-readable display name, e.g. "Create a git
	// commit". It is NOT the description -- Description stays the long form.
	Title string
	// ReadOnly reports that the tool does not modify its environment.
	// Claude Code's plan mode runs such tools with no permission prompt.
	//
	// Scope note: it is also set on tools whose only writes land on
	// gitignored plugin state (.sdlc-v2/runs, .sdlc-v2/learnings) or
	// os.TempDir, on paths the plugin computes itself and a caller cannot
	// redirect. A tool that can write ANY git-tracked file must leave this
	// false. See docs/mcp-tool-annotations.md.
	ReadOnly bool
	// Destructive reports that the tool may perform destructive (non-additive)
	// updates. When in doubt, declare true -- over-restricting is safe.
	Destructive bool
	// Idempotent reports that repeating a call with the same arguments has no
	// further effect. When in doubt, declare false.
	Idempotent bool
	// OpenWorld reports that the tool reaches external entities (network).
	OpenWorld bool
}

// apply writes a fully-populated annotation block onto the tool.
func (a Annotations) apply(t *mcp.Tool) {
	// Addressed locals: DestructiveHint and OpenWorldHint are *bool with
	// omitempty and default to TRUE when nil, so they are never left unset.
	destructive, openWorld := a.Destructive, a.OpenWorld
	t.Title = a.Title // canonical slot (spec 2025-06-18)
	t.Annotations = &mcp.ToolAnnotations{
		Title:           a.Title, // fallback slot, older clients
		ReadOnlyHint:    a.ReadOnly,
		DestructiveHint: &destructive,
		IdempotentHint:  a.Idempotent,
		OpenWorldHint:   &openWorld,
	}
}

// Register adds a typed tool to the server. TIn defines the JSON schema for
// the tool's input (derived from struct tags). The handler receives a
// deserialized TIn and returns TOut, which is wrapped in a KD3 envelope.
//
// a carries the tool's MCP annotations and is required -- there is no
// zero-value default, because Go's false zero value for Destructive and
// OpenWorld would silently under-claim danger (the MCP spec defaults both to
// true). Classify the tool against the ReadOnly boundary rule before filling
// it in, and add the matching row to the toolAnnotations golden map in
// internal/tools/annotations_test.go; see docs/mcp-tool-annotations.md.
//
// All handler outcomes -- success, typed errors, panics -- are returned as
// text content in the MCP result with appropriate envelope and IsError flag.
// The handler never returns a Go error, so the protocol layer never sees a
// JSON-RPC error from tool execution. Input is unmarshaled and validated
// manually (rather than via the SDK's generic AddTool) so that invalid input
// can be reported through the KD3 "data" error envelope instead of a
// protocol-level error.
func Register[TIn, TOut any](s *Server, name, desc string, a Annotations, h func(ctx Ctx, in TIn) (TOut, error)) {
	inSchema, err := schemaFor[TIn]()
	if err != nil {
		panic(fmt.Sprintf("mcpserver: register %q: input schema: %v", name, err))
	}
	outSchema, err := schemaFor[OKEnvelope[TOut]]()
	if err != nil {
		panic(fmt.Sprintf("mcpserver: register %q: output schema: %v", name, err))
	}

	tool := &mcp.Tool{
		Name:         name,
		Description:  desc,
		InputSchema:  inSchema,
		OutputSchema: outSchema,
	}

	a.apply(tool)

	handler := func(_ context.Context, req *mcp.CallToolRequest) (result *mcp.CallToolResult, _ error) {
		// Fresh per-call warning dedup.
		warnings := NewDedup()

		// Build Ctx.
		sessionID := ""
		if req.Session != nil {
			sessionID = req.Session.ID()
		}
		tCtx := Ctx{
			SessionID: sessionID,
			Quiet:     false,
			Warnings:  warnings,
		}

		// Panic recovery -- convert to code:"infra" envelope.
		defer func() {
			if r := recover(); r != nil {
				msg := fmt.Sprintf("panic: %v", r)
				env, marshalErr := wrapErr("infra", msg, "")
				if marshalErr != nil {
					// Last resort: raw text.
					env = []byte(`{"ok":false,"code":"infra","error":"panic recovery marshal failure"}`)
				}
				result = &mcp.CallToolResult{
					Content: []mcp.Content{
						&mcp.TextContent{Text: string(env)},
					},
					IsError: true,
				}
			}
		}()

		// Deserialize input.
		var in TIn
		if len(req.Params.Arguments) > 0 {
			if err := json.Unmarshal(req.Params.Arguments, &in); err != nil {
				code, errMsg := "data", fmt.Sprintf("invalid input: %s", err.Error())
				env, marshalErr := wrapErr(code, errMsg, "")
				if marshalErr != nil {
					env = []byte(`{"ok":false,"code":"data","error":"input bind failure"}`)
				}
				return &mcp.CallToolResult{
					Content: []mcp.Content{
						&mcp.TextContent{Text: string(env)},
					},
					IsError: true,
				}, nil
			}
		}

		// Call typed handler.
		out, err := h(tCtx, in)
		if err != nil {
			code, errMsg, suggestion := mapError(err)
			env, marshalErr := wrapErr(code, errMsg, suggestion)
			if marshalErr != nil {
				env = []byte(`{"ok":false,"code":"infra","error":"error envelope marshal failure"}`)
			}
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: string(env)},
				},
				IsError: true,
			}, nil
		}

		// Success envelope.
		env, marshalErr := wrapOK(out)
		if marshalErr != nil {
			errEnv, _ := wrapErr("infra", fmt.Sprintf("marshal result: %s", marshalErr.Error()), "")
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: string(errEnv)},
				},
				IsError: true,
			}, nil
		}

		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: string(env)},
			},
			StructuredContent: OKEnvelope[TOut]{OK: true, Data: out},
			IsError:           false,
		}, nil
	}

	s.mcp.AddTool(tool, handler)
}
