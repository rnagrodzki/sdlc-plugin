package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// --- test input / output types ---

type echoIn struct {
	Msg string `json:"msg"`
}

type echoOut struct {
	Reply string `json:"reply"`
}

// --- helpers ---

// envelope is the parsed KD3 envelope from tool results.
type envelope struct {
	OK    bool            `json:"ok"`
	Data  json.RawMessage `json:"data,omitempty"`
	Code  string          `json:"code,omitempty"`
	Error string          `json:"error,omitempty"`
}

func callTool(t *testing.T, c *mcp.ClientSession, name string, args map[string]any) (*mcp.CallToolResult, envelope) {
	t.Helper()
	result, err := c.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool %q: %v", name, err)
	}
	if len(result.Content) == 0 {
		t.Fatalf("CallTool %q: no content", name)
	}
	text, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("CallTool %q: content[0] not TextContent, got %T", name, result.Content[0])
	}

	var env envelope
	if err := json.Unmarshal([]byte(text.Text), &env); err != nil {
		t.Fatalf("CallTool %q: unmarshal envelope: %v\nraw: %s", name, err, text.Text)
	}
	return result, env
}

func connectInMemory(t *testing.T, srv *Server) *mcp.ClientSession {
	t.Helper()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()

	ctx := context.Background()
	if _, err := srv.mcp.Connect(ctx, serverTransport, nil); err != nil {
		t.Fatalf("server Connect: %v", err)
	}

	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0.0.0"}, nil)
	cs, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client Connect: %v", err)
	}
	t.Cleanup(func() { cs.Close() })

	return cs
}

func setupClient(t *testing.T) *mcp.ClientSession {
	t.Helper()
	srv := New("test", "0.0.0-test")

	// Register tools in NON-alphabetical order to verify stable ordering.
	Register(srv, "zz-echo", "echoes input", func(ctx Ctx, in echoIn) (echoOut, error) {
		return echoOut{Reply: "echo:" + in.Msg}, nil
	})

	Register(srv, "aa-domain-err", "returns domain error", func(ctx Ctx, in echoIn) (echoOut, error) {
		return echoOut{}, &DomainError{Msg: "bad request"}
	})

	Register(srv, "bb-infra-err", "returns infra error", func(ctx Ctx, in echoIn) (echoOut, error) {
		return echoOut{}, &InfraError{Msg: "connection refused"}
	})

	Register(srv, "cc-data-err", "returns data error", func(ctx Ctx, in echoIn) (echoOut, error) {
		return echoOut{}, &DataError{Msg: "schema mismatch"}
	})

	Register(srv, "dd-panic", "panics", func(ctx Ctx, in echoIn) (echoOut, error) {
		panic("kaboom")
	})

	Register(srv, "ee-unknown-err", "returns untyped error", func(ctx Ctx, in echoIn) (echoOut, error) {
		return echoOut{}, fmt.Errorf("something broke")
	})

	return connectInMemory(t, srv)
}

// --- tests ---

func TestListToolsOrdering(t *testing.T) {
	c := setupClient(t)

	resp, err := c.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if resp == nil {
		t.Fatal("ListTools: nil response")
	}

	want := []string{"aa-domain-err", "bb-infra-err", "cc-data-err", "dd-panic", "ee-unknown-err", "zz-echo"}
	if len(resp.Tools) != len(want) {
		t.Fatalf("ListTools: got %d tools, want %d", len(resp.Tools), len(want))
	}
	for i, name := range want {
		if resp.Tools[i].Name != name {
			t.Errorf("ListTools[%d]: got %q, want %q", i, resp.Tools[i].Name, name)
		}
	}

	// Verify schema for zz-echo contains "msg" property with type string.
	var echoTool *mcp.Tool
	for _, tool := range resp.Tools {
		if tool.Name == "zz-echo" {
			echoTool = tool
			break
		}
	}
	schemaBytes, err := json.Marshal(echoTool.InputSchema)
	if err != nil {
		t.Fatalf("marshal schema: %v", err)
	}
	schemaStr := string(schemaBytes)
	// InputSchema should reference "msg" property.
	var schema map[string]any
	if err := json.Unmarshal(schemaBytes, &schema); err != nil {
		t.Fatalf("unmarshal schema: %v", err)
	}
	props, _ := schema["properties"].(map[string]any)
	msgProp, ok := props["msg"].(map[string]any)
	if !ok {
		t.Fatalf("schema missing 'msg' property: %s", schemaStr)
	}
	if msgProp["type"] != "string" {
		t.Errorf("msg type = %v, want \"string\" (schema: %s)", msgProp["type"], schemaStr)
	}

	// Call twice to verify stable ordering.
	resp2, err := c.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools (2nd): %v", err)
	}
	for i := range want {
		if resp.Tools[i].Name != resp2.Tools[i].Name {
			t.Errorf("ListTools ordering unstable at [%d]: %q vs %q", i, resp.Tools[i].Name, resp2.Tools[i].Name)
		}
	}
}

func TestSuccessEnvelope(t *testing.T) {
	c := setupClient(t)
	result, env := callTool(t, c, "zz-echo", map[string]any{"msg": "hello"})

	if result.IsError {
		t.Error("IsError should be false for success")
	}
	if !env.OK {
		t.Error("envelope.ok should be true")
	}

	var data echoOut
	if err := json.Unmarshal(env.Data, &data); err != nil {
		t.Fatalf("unmarshal data: %v", err)
	}
	if data.Reply != "echo:hello" {
		t.Errorf("reply = %q, want %q", data.Reply, "echo:hello")
	}
}

func TestDomainError(t *testing.T) {
	c := setupClient(t)
	result, env := callTool(t, c, "aa-domain-err", map[string]any{"msg": "x"})

	if !result.IsError {
		t.Error("IsError should be true for domain error")
	}
	if env.OK {
		t.Error("envelope.ok should be false")
	}
	if env.Code != "domain" {
		t.Errorf("code = %q, want %q", env.Code, "domain")
	}
	if env.Error != "bad request" {
		t.Errorf("error = %q, want %q", env.Error, "bad request")
	}
}

func TestInfraError(t *testing.T) {
	c := setupClient(t)
	result, env := callTool(t, c, "bb-infra-err", map[string]any{"msg": "x"})

	if !result.IsError {
		t.Error("IsError should be true for infra error")
	}
	if env.OK {
		t.Error("envelope.ok should be false")
	}
	if env.Code != "infra" {
		t.Errorf("code = %q, want %q", env.Code, "infra")
	}
}

func TestDataError(t *testing.T) {
	c := setupClient(t)
	result, env := callTool(t, c, "cc-data-err", map[string]any{"msg": "x"})

	if !result.IsError {
		t.Error("IsError should be true for data error")
	}
	if env.OK {
		t.Error("envelope.ok should be false")
	}
	if env.Code != "data" {
		t.Errorf("code = %q, want %q", env.Code, "data")
	}
}

func TestPanicRecovery(t *testing.T) {
	c := setupClient(t)
	result, env := callTool(t, c, "dd-panic", map[string]any{"msg": "x"})

	if !result.IsError {
		t.Error("IsError should be true for panic")
	}
	if env.OK {
		t.Error("envelope.ok should be false")
	}
	if env.Code != "infra" {
		t.Errorf("code = %q, want %q", env.Code, "infra")
	}
	if env.Error != "panic: kaboom" {
		t.Errorf("error = %q, want %q", env.Error, "panic: kaboom")
	}

	// Server must survive -- a subsequent call should succeed.
	result2, env2 := callTool(t, c, "zz-echo", map[string]any{"msg": "after-panic"})
	if result2.IsError {
		t.Error("server should survive panic: IsError true on follow-up call")
	}
	if !env2.OK {
		t.Error("server should survive panic: envelope.ok false on follow-up call")
	}
}

func TestUnknownErrorMapsToInfra(t *testing.T) {
	c := setupClient(t)
	result, env := callTool(t, c, "ee-unknown-err", map[string]any{"msg": "x"})

	if !result.IsError {
		t.Error("IsError should be true for unknown error")
	}
	if env.Code != "infra" {
		t.Errorf("code = %q, want %q", env.Code, "infra")
	}
}

func TestBadInputMapsToData(t *testing.T) {
	c := setupClient(t)
	// Send wrong type for "msg" field (number instead of string).
	result, env := callTool(t, c, "zz-echo", map[string]any{"msg": 42})

	if !result.IsError {
		t.Error("IsError should be true for bad input")
	}
	if env.Code != "data" {
		t.Errorf("code = %q, want %q", env.Code, "data")
	}
}

func TestDedupAdd(t *testing.T) {
	d := NewDedup()
	if !d.Add("warn1") {
		t.Error("first Add should return true")
	}
	if d.Add("warn1") {
		t.Error("second Add of same message should return false")
	}
	if !d.Add("warn2") {
		t.Error("Add of different message should return true")
	}
}

func TestWarningsPerCallAndSessionID(t *testing.T) {
	srv := New("test", "0.0.0-test")

	var firstDedup, secondDedup *Dedup
	var firstSessionID, secondSessionID string
	calls := 0
	Register(srv, "capture-dedup", "captures dedup ref", func(ctx Ctx, in echoIn) (echoOut, error) {
		calls++
		if calls == 1 {
			firstDedup = ctx.Warnings
			firstSessionID = ctx.SessionID
		} else {
			secondDedup = ctx.Warnings
			secondSessionID = ctx.SessionID
		}
		return echoOut{Reply: "ok"}, nil
	})

	c := connectInMemory(t, srv)

	callTool(t, c, "capture-dedup", map[string]any{"msg": "a"})
	callTool(t, c, "capture-dedup", map[string]any{"msg": "b"})

	if firstDedup == secondDedup {
		t.Error("each call should get a fresh Dedup instance")
	}

	// The in-memory/stdio transports don't assign a protocol session ID
	// (only HTTP-based transports do), so Ctx.SessionID is consistently
	// empty within a connection -- callers fall back to
	// telemetry.ResolveSessionID's marker-file/env/PID chain instead.
	if firstSessionID != secondSessionID {
		t.Errorf("Ctx.SessionID should be stable within a connection: %q vs %q", firstSessionID, secondSessionID)
	}
}
