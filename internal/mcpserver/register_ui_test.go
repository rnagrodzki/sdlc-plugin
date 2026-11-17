package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The view fixture that the RegisterUI tests share.
const (
	uiTestURI  = "ui://sdlc/test-card"
	uiTestName = "test-card"
	uiTestHTML = "<!doctype html><html><body><script>/* view */</script></body></html>"
)

// uiTestResource returns the view that the RegisterUI fixtures use.
func uiTestResource() UIResource {
	return UIResource{URI: uiTestURI, Name: uiTestName, HTML: uiTestHTML}
}

// uiEcho is the success handler shared by the Register and RegisterUI fixtures.
func uiEcho(_ Ctx, in echoIn) (echoOut, error) {
	return echoOut{Reply: "echo:" + in.Msg}, nil
}

// newUIServer returns a server with one RegisterUI tool named name and handler h.
func newUIServer(name string, h func(Ctx, echoIn) (echoOut, error)) *Server {
	srv := New("test", "0.0.0-test")
	RegisterUI(srv, name, "ui fixture", registryAnnotations(), uiTestResource(), h)
	return srv
}

// mustPanic runs f and returns the panic value as a string. The test fails
// when f does not panic.
func mustPanic(t *testing.T, f func()) (msg string) {
	t.Helper()
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected a panic, got none")
		}
		msg, _ = r.(string)
	}()
	f()
	return ""
}

// toJSONValue round-trips v through JSON, so it compares equal to a value
// that a client decoded from the wire.
func toJSONValue(t *testing.T, v any) any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return out
}

// TestRegisterUI_ToolMetaNamesResource verifies that the tool _meta carries
// ui.resourceUri, and that a Register tool has no such _meta.
func TestRegisterUI_ToolMetaNamesResource(t *testing.T) {
	srv := newUIServer("ui_echo", uiEcho)
	Register(srv, "plain_echo", "plain fixture", registryAnnotations(), uiEcho)
	c := connectInMemory(t, srv)

	resp, err := c.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	byName := map[string]*mcp.Tool{}
	for _, tool := range resp.Tools {
		byName[tool.Name] = tool
	}

	want := map[string]any{"resourceUri": uiTestURI}
	if got := byName["ui_echo"].Meta["ui"]; !reflect.DeepEqual(got, want) {
		t.Errorf("ui_echo _meta.ui = %#v, want %#v", got, want)
	}
	if got, ok := byName["plain_echo"].Meta["ui"]; ok {
		t.Errorf("plain_echo _meta.ui = %#v, want absent", got)
	}
}

// TestRegisterUI_ResourceRead verifies that resources/read of the view URI
// returns one content with the MCP App MIME type and the HTML text.
func TestRegisterUI_ResourceRead(t *testing.T) {
	c := connectInMemory(t, newUIServer("ui_echo", uiEcho))

	res, err := c.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: uiTestURI})
	if err != nil {
		t.Fatalf("ReadResource: %v", err)
	}
	if len(res.Contents) != 1 {
		t.Fatalf("contents = %d, want 1", len(res.Contents))
	}
	got := res.Contents[0]
	if got.URI != uiTestURI || got.MIMEType != MCPAppMIMEType || got.Text != uiTestHTML {
		t.Errorf("content = {URI:%q MIMEType:%q Text:%q}, want {URI:%q MIMEType:%q Text:%q}",
			got.URI, got.MIMEType, got.Text, uiTestURI, MCPAppMIMEType, uiTestHTML)
	}

	list, err := c.ListResources(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	if len(list.Resources) != 1 {
		t.Fatalf("resources = %d, want 1", len(list.Resources))
	}
	r := list.Resources[0]
	if r.URI != uiTestURI || r.Name != uiTestName || r.MIMEType != MCPAppMIMEType {
		t.Errorf("resource = {URI:%q Name:%q MIMEType:%q}, want {URI:%q Name:%q MIMEType:%q}",
			r.URI, r.Name, r.MIMEType, uiTestURI, uiTestName, MCPAppMIMEType)
	}
}

// TestRegisterUI_SuccessAddsStructuredContent covers the result-table row
// "handler returns Out, nil": the same text as Register, plus Out as
// structuredContent.
func TestRegisterUI_SuccessAddsStructuredContent(t *testing.T) {
	plain := New("test", "0.0.0-test")
	Register(plain, "same_echo", "fixture", registryAnnotations(), uiEcho)
	plainResult, _ := callTool(t, connectInMemory(t, plain), "same_echo", map[string]any{"msg": "hi"})

	result, env := callTool(t, connectInMemory(t, newUIServer("same_echo", uiEcho)), "same_echo", map[string]any{"msg": "hi"})

	if result.IsError || !env.OK {
		t.Fatalf("IsError = %v, ok = %v, want a success", result.IsError, env.OK)
	}
	gotText := result.Content[0].(*mcp.TextContent).Text
	wantText := plainResult.Content[0].(*mcp.TextContent).Text
	if gotText != wantText {
		t.Errorf("content[0].text differs from Register:\n got: %q\nwant: %q", gotText, wantText)
	}
	want := toJSONValue(t, echoOut{Reply: "echo:hi"})
	if !reflect.DeepEqual(result.StructuredContent, want) {
		t.Errorf("StructuredContent = %#v, want %#v", result.StructuredContent, want)
	}
}

// TestRegisterUI_HandlerErrorIsTextOnly covers the result-table row "handler
// returns an error".
func TestRegisterUI_HandlerErrorIsTextOnly(t *testing.T) {
	srv := newUIServer("ui_err", func(Ctx, echoIn) (echoOut, error) {
		return echoOut{}, &DomainError{Msg: "bad request", Suggestion: "Pass a non-empty msg, then retry."}
	})
	result, env := callTool(t, connectInMemory(t, srv), "ui_err", map[string]any{"msg": "x"})

	if !result.IsError || env.Code != "domain" {
		t.Errorf("IsError = %v, code = %q, want true, domain", result.IsError, env.Code)
	}
	if result.StructuredContent != nil {
		t.Errorf("StructuredContent = %#v, want nil", result.StructuredContent)
	}
}

// TestRegisterUI_BadInputIsTextOnly covers the result-table row "arguments do
// not decode".
func TestRegisterUI_BadInputIsTextOnly(t *testing.T) {
	result, env := callTool(t, connectInMemory(t, newUIServer("ui_echo", uiEcho)), "ui_echo", map[string]any{"msg": 123})

	if !result.IsError || env.Code != "data" {
		t.Errorf("IsError = %v, code = %q, want true, data", result.IsError, env.Code)
	}
	if !strings.Contains(env.Body, "invalid input") {
		t.Errorf("body missing invalid input message, got: %s", env.Body)
	}
	if result.StructuredContent != nil {
		t.Errorf("StructuredContent = %#v, want nil", result.StructuredContent)
	}
}

// TestRegisterUI_HandlerPanicIsTextOnly covers the result-table row "handler
// panics".
func TestRegisterUI_HandlerPanicIsTextOnly(t *testing.T) {
	srv := newUIServer("ui_panic", func(Ctx, echoIn) (echoOut, error) {
		panic("boom")
	})
	result, env := callTool(t, connectInMemory(t, srv), "ui_panic", map[string]any{"msg": "x"})

	if !result.IsError || env.Code != "infra" {
		t.Errorf("IsError = %v, code = %q, want true, infra", result.IsError, env.Code)
	}
	if !strings.Contains(env.Body, "panic: boom") {
		t.Errorf("body missing panic message, got: %s", env.Body)
	}
	if result.StructuredContent != nil {
		t.Errorf("StructuredContent = %#v, want nil", result.StructuredContent)
	}
}

// TestRegisterUI_URIWithoutUIScheme_Panics covers the result-table row
// "registration: ui.URI without ui://". Nothing is registered.
func TestRegisterUI_URIWithoutUIScheme_Panics(t *testing.T) {
	srv := New("test", "0.0.0-test")
	ui := uiTestResource()
	ui.URI = "https://example.com/card"

	msg := mustPanic(t, func() {
		RegisterUI(srv, "ui_echo", "fixture", registryAnnotations(), ui, uiEcho)
	})
	const want = `mcpserver: register "ui_echo": view URI "https://example.com/card" does not start with ui://`
	if msg != want {
		t.Errorf("panic message = %q, want %q", msg, want)
	}
	if names := srv.ToolNames(); len(names) != 0 {
		t.Errorf("ToolNames = %v, want none", names)
	}
}

// TestRegisterUI_HTMLSizeBoundary covers the result-table row "registration:
// len(ui.HTML) above MaxUIHTMLBytes": exactly MaxUIHTMLBytes registers, one
// byte more panics and registers nothing.
func TestRegisterUI_HTMLSizeBoundary(t *testing.T) {
	t.Run("at limit registers", func(t *testing.T) {
		srv := New("test", "0.0.0-test")
		ui := uiTestResource()
		ui.HTML = strings.Repeat("a", MaxUIHTMLBytes)
		RegisterUI(srv, "ui_echo", "fixture", registryAnnotations(), ui, uiEcho)

		if names := srv.ToolNames(); !reflect.DeepEqual(names, []string{"ui_echo"}) {
			t.Errorf("ToolNames = %v, want [ui_echo]", names)
		}
		res, err := connectInMemory(t, srv).ReadResource(context.Background(), &mcp.ReadResourceParams{URI: uiTestURI})
		if err != nil {
			t.Fatalf("ReadResource: %v", err)
		}
		if got := len(res.Contents[0].Text); got != MaxUIHTMLBytes {
			t.Errorf("resource text = %d bytes, want %d", got, MaxUIHTMLBytes)
		}
	})

	t.Run("one byte above limit panics", func(t *testing.T) {
		srv := New("test", "0.0.0-test")
		ui := uiTestResource()
		ui.HTML = strings.Repeat("a", MaxUIHTMLBytes+1)

		msg := mustPanic(t, func() {
			RegisterUI(srv, "ui_echo", "fixture", registryAnnotations(), ui, uiEcho)
		})
		want := fmt.Sprintf("mcpserver: register %q: view HTML is %d bytes, above the limit of %d", "ui_echo", MaxUIHTMLBytes+1, MaxUIHTMLBytes)
		if msg != want {
			t.Errorf("panic message = %q, want %q", msg, want)
		}
		if names := srv.ToolNames(); len(names) != 0 {
			t.Errorf("ToolNames = %v, want none", names)
		}
	})
}
