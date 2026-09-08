package mcpserver

import (
	"context"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// Ctx carries per-call context passed to typed tool handlers.
type Ctx struct {
	SessionID string
	Quiet     bool // Reserved for future use; defaults to false.
	Warnings  *Dedup
}

// Register adds a typed tool to the server. TIn defines the JSON schema for
// the tool's input (derived from struct tags). The handler receives a
// deserialized TIn and returns TOut, which is wrapped in a KD3 envelope.
//
// All handler outcomes -- success, typed errors, panics -- are returned as
// text content in the MCP result with appropriate envelope and IsError flag.
// The underlying mcp-go ToolHandlerFunc never returns a Go error, so the
// protocol layer never sees a JSON-RPC error from tool execution.
func Register[TIn, TOut any](s *Server, name, desc string, h func(ctx Ctx, in TIn) (TOut, error)) {
	tool := mcp.NewTool(name,
		mcp.WithDescription(desc),
		mcp.WithInputSchema[TIn](),
		mcp.WithOutputSchema[OKEnvelope[TOut]](),
	)

	handler := func(goCtx context.Context, req mcp.CallToolRequest) (result *mcp.CallToolResult, _ error) {
		// Fresh per-call warning dedup.
		warnings := NewDedup()

		// Build Ctx.
		sessionID := ""
		if sess := server.ClientSessionFromContext(goCtx); sess != nil {
			sessionID = sess.SessionID()
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
				env, marshalErr := wrapErr("infra", msg)
				if marshalErr != nil {
					// Last resort: raw text.
					env = []byte(`{"ok":false,"code":"infra","error":"panic recovery marshal failure"}`)
				}
				result = &mcp.CallToolResult{
					Content: []mcp.Content{
						mcp.TextContent{Type: "text", Text: string(env)},
					},
					IsError: true,
				}
			}
		}()

		// Deserialize input.
		var in TIn
		if err := req.BindArguments(&in); err != nil {
			code, errMsg := "data", fmt.Sprintf("invalid input: %s", err.Error())
			env, marshalErr := wrapErr(code, errMsg)
			if marshalErr != nil {
				env = []byte(`{"ok":false,"code":"data","error":"input bind failure"}`)
			}
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					mcp.TextContent{Type: "text", Text: string(env)},
				},
				IsError: true,
			}, nil
		}

		// Call typed handler.
		out, err := h(tCtx, in)
		if err != nil {
			code, errMsg := mapError(err)
			env, marshalErr := wrapErr(code, errMsg)
			if marshalErr != nil {
				env = []byte(`{"ok":false,"code":"infra","error":"error envelope marshal failure"}`)
			}
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					mcp.TextContent{Type: "text", Text: string(env)},
				},
				IsError: true,
			}, nil
		}

		// Success envelope.
		env, marshalErr := wrapOK(out)
		if marshalErr != nil {
			errEnv, _ := wrapErr("infra", fmt.Sprintf("marshal result: %s", marshalErr.Error()))
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					mcp.TextContent{Type: "text", Text: string(errEnv)},
				},
				IsError: true,
			}, nil
		}

		return &mcp.CallToolResult{
			Content: []mcp.Content{
				mcp.TextContent{Type: "text", Text: string(env)},
			},
			StructuredContent: OKEnvelope[TOut]{OK: true, Data: out},
			IsError:           false,
		}, nil
	}

	s.mcp.AddTool(tool, handler)
}
