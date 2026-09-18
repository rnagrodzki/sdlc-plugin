package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
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

// renderedResult is the parsed first line and body of a rendered tool
// result: "# <tool> — ok" or "# <tool> — error (<code>)", per render.go /
// envelope.go's renderOK / renderError.
type renderedResult struct {
	Tool string
	OK   bool
	Code string
	Body string
}

// resultHeadRe pins the renderer's first-line contract from Task 1.
var resultHeadRe = regexp.MustCompile(`^# ([a-z_]+) — (?:(ok)|error \((domain|infra|data)\))$`)

func callTool(t *testing.T, c *mcp.ClientSession, name string, args map[string]any) (*mcp.CallToolResult, renderedResult) {
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
	head, body, _ := strings.Cut(text.Text, "\n")
	m := resultHeadRe.FindStringSubmatch(head)
	if m == nil {
		t.Fatalf("CallTool %q: first line %q does not match %s", name, head, resultHeadRe)
	}
	return result, renderedResult{Tool: m[1], OK: m[2] == "ok", Code: m[3], Body: body}
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
	Register(srv, "zz_echo", "echoes input", Annotations{
		Title:      "Echo input",
		ReadOnly:   true,
		Idempotent: true,
	}, func(ctx Ctx, in echoIn) (echoOut, error) {
		return echoOut{Reply: "echo:" + in.Msg}, nil
	})

	Register(srv, "aa_domain_err", "returns domain error", Annotations{
		Title:      "Return domain error",
		ReadOnly:   true,
		Idempotent: true,
	}, func(ctx Ctx, in echoIn) (echoOut, error) {
		return echoOut{}, &DomainError{Msg: "bad request"}
	})

	Register(srv, "bb_infra_err", "returns infra error", Annotations{
		Title:      "Return infra error",
		ReadOnly:   true,
		Idempotent: true,
	}, func(ctx Ctx, in echoIn) (echoOut, error) {
		return echoOut{}, &InfraError{Msg: "connection refused"}
	})

	Register(srv, "cc_data_err", "returns data error", Annotations{
		Title:      "Return data error",
		ReadOnly:   true,
		Idempotent: true,
	}, func(ctx Ctx, in echoIn) (echoOut, error) {
		return echoOut{}, &DataError{Msg: "schema mismatch"}
	})

	Register(srv, "dd_panic", "panics", Annotations{
		Title:      "Panic for testing",
		ReadOnly:   true,
		Idempotent: true,
	}, func(ctx Ctx, in echoIn) (echoOut, error) {
		panic("kaboom")
	})

	Register(srv, "ee_unknown_err", "returns untyped error", Annotations{
		Title:      "Return untyped error",
		ReadOnly:   true,
		Idempotent: true,
	}, func(ctx Ctx, in echoIn) (echoOut, error) {
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

	want := []string{"aa_domain_err", "bb_infra_err", "cc_data_err", "dd_panic", "ee_unknown_err", "zz_echo"}
	if len(resp.Tools) != len(want) {
		t.Fatalf("ListTools: got %d tools, want %d", len(resp.Tools), len(want))
	}
	for i, name := range want {
		if resp.Tools[i].Name != name {
			t.Errorf("ListTools[%d]: got %q, want %q", i, resp.Tools[i].Name, name)
		}
	}

	// Verify schema for zz_echo contains "msg" property with type string.
	var echoTool *mcp.Tool
	for _, tool := range resp.Tools {
		if tool.Name == "zz_echo" {
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
	result, env := callTool(t, c, "zz_echo", map[string]any{"msg": "hello"})

	if result.IsError {
		t.Error("IsError should be false for success")
	}
	if !env.OK {
		t.Error("rendered result should be ok")
	}
	if !strings.Contains(env.Body, "- reply: echo:hello") {
		t.Errorf("body missing reply bullet, got: %s", env.Body)
	}
}

func TestDomainError(t *testing.T) {
	c := setupClient(t)
	result, env := callTool(t, c, "aa_domain_err", map[string]any{"msg": "x"})

	if !result.IsError {
		t.Error("IsError should be true for domain error")
	}
	if env.OK {
		t.Error("rendered result should not be ok")
	}
	if env.Code != "domain" {
		t.Errorf("code = %q, want %q", env.Code, "domain")
	}
	if !strings.Contains(env.Body, "## What happened\nbad request\n") {
		t.Errorf("body missing error message, got: %s", env.Body)
	}
}

func TestInfraError(t *testing.T) {
	c := setupClient(t)
	result, env := callTool(t, c, "bb_infra_err", map[string]any{"msg": "x"})

	if !result.IsError {
		t.Error("IsError should be true for infra error")
	}
	if env.OK {
		t.Error("rendered result should not be ok")
	}
	if env.Code != "infra" {
		t.Errorf("code = %q, want %q", env.Code, "infra")
	}
}

func TestDataError(t *testing.T) {
	c := setupClient(t)
	result, env := callTool(t, c, "cc_data_err", map[string]any{"msg": "x"})

	if !result.IsError {
		t.Error("IsError should be true for data error")
	}
	if env.OK {
		t.Error("rendered result should not be ok")
	}
	if env.Code != "data" {
		t.Errorf("code = %q, want %q", env.Code, "data")
	}
}

func TestPanicRecovery(t *testing.T) {
	c := setupClient(t)
	result, env := callTool(t, c, "dd_panic", map[string]any{"msg": "x"})

	if !result.IsError {
		t.Error("IsError should be true for panic")
	}
	if env.OK {
		t.Error("rendered result should not be ok")
	}
	if env.Code != "infra" {
		t.Errorf("code = %q, want %q", env.Code, "infra")
	}
	if !strings.Contains(env.Body, "## What happened\npanic: kaboom\n") {
		t.Errorf("body missing panic message, got: %s", env.Body)
	}

	// Server must survive -- a subsequent call should succeed.
	result2, env2 := callTool(t, c, "zz_echo", map[string]any{"msg": "after-panic"})
	if result2.IsError {
		t.Error("server should survive panic: IsError true on follow-up call")
	}
	if !env2.OK {
		t.Error("server should survive panic: follow-up call should be ok")
	}
}

func TestUnknownErrorMapsToInfra(t *testing.T) {
	c := setupClient(t)
	result, env := callTool(t, c, "ee_unknown_err", map[string]any{"msg": "x"})

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
	result, env := callTool(t, c, "zz_echo", map[string]any{"msg": 42})

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
	Register(srv, "capture_dedup", "captures dedup ref", Annotations{
		Title:      "Capture dedup",
		ReadOnly:   true,
		Idempotent: true,
	}, func(ctx Ctx, in echoIn) (echoOut, error) {
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

	callTool(t, c, "capture_dedup", map[string]any{"msg": "a"})
	callTool(t, c, "capture_dedup", map[string]any{"msg": "b"})

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
