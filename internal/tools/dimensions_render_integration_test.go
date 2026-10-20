//go:build integration

package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rnagrodzki/sdlc-plugin/internal/dimensions"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
)

// callRegisteredDimensionsRender runs the registered
// dimensions_render_instructions tool over an in-memory MCP session and
// returns the result and its text. The call passes projectRoot, so the
// handler does not resolve the active worktree.
func callRegisteredDimensionsRender(t *testing.T, args map[string]any) (*mcp.CallToolResult, string) {
	t.Helper()
	s := mcpserver.New("test", "0.0.0-test")
	RegisterDimensionsRenderTools(s)
	ctx := context.Background()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	if _, err := s.MCPServer().Connect(ctx, serverTransport, nil); err != nil {
		t.Fatalf("server Connect: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0.0.0"}, nil)
	c, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client Connect: %v", err)
	}
	t.Cleanup(func() { c.Close() })

	res, err := c.CallTool(ctx, &mcp.CallToolParams{Name: "dimensions_render_instructions", Arguments: args})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if len(res.Content) == 0 {
		t.Fatal("CallTool: no content")
	}
	text, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("content[0] is %T, want *mcp.TextContent", res.Content[0])
	}
	return res, text.Text
}

// TestIntegration_DimensionsRender_RefusesHandWrittenMirror calls the
// registered dimensions_render_instructions tool in render mode over four
// states of one mirror. An absent mirror is written with a marker line. A
// second render replaces the generated mirror with the same bytes. A mirror
// with hand-written rows is refused, the tool error names the mirror and
// overwrite, and the file bytes stay the same. A call with overwrite true
// replaces it, and a later call without overwrite then succeeds.
func TestIntegration_DimensionsRender_RefusesHandWrittenMirror(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "dim.md"), mirrorGuardDimension)
	mirror := filepath.Join(root, ".github", "instructions", "security.instructions.md")
	args := map[string]any{"file": "dim.md", "projectRoot": root}
	withOverwrite := map[string]any{"file": "dim.md", "projectRoot": root, "overwrite": true}

	readMirror := func() string {
		t.Helper()
		got, err := os.ReadFile(mirror)
		if err != nil {
			t.Fatal(err)
		}
		return string(got)
	}

	res, text := callRegisteredDimensionsRender(t, args)
	if res.IsError {
		t.Fatalf("render to an absent mirror returned a tool error:\n%s", text)
	}
	if want := "Rendered to " + mirror + "."; !strings.Contains(text, want) {
		t.Errorf("output missing %q:\n%s", want, text)
	}
	generated := readMirror()
	if got := dimensions.CheckMirror([]byte(generated)); got != dimensions.MirrorGenerated {
		t.Fatalf("CheckMirror(first render) = %v, want MirrorGenerated", got)
	}

	if res, text = callRegisteredDimensionsRender(t, args); res.IsError {
		t.Fatalf("render over a generated mirror returned a tool error:\n%s", text)
	}
	if got := readMirror(); got != generated {
		t.Errorf("second render changed the bytes\n--- first ---\n%s\n--- second ---\n%s", generated, got)
	}

	handWritten := "# my own rows\n\n- keep this row\n"
	writeFile(t, mirror, handWritten)
	res, text = callRegisteredDimensionsRender(t, args)
	if !res.IsError {
		t.Fatalf("render over a hand-written mirror succeeded:\n%s", text)
	}
	for _, want := range []string{
		"error (domain)",
		mirror,
		"has hand-written content, or an sdlc version older than the marker wrote it (no sdlc:generated marker line at the end of the file), and was not replaced",
		"overwrite: true",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("refusal output missing %q:\n%s", want, text)
		}
	}
	if got := readMirror(); got != handWritten {
		t.Errorf("a refused render changed the mirror:\n%s", got)
	}

	res, text = callRegisteredDimensionsRender(t, withOverwrite)
	if res.IsError {
		t.Fatalf("render with overwrite returned a tool error:\n%s", text)
	}
	if got := readMirror(); got != generated {
		t.Errorf("render with overwrite did not replace the mirror\n--- got ---\n%s\n--- want ---\n%s", got, generated)
	}
	if res, text = callRegisteredDimensionsRender(t, args); res.IsError {
		t.Fatalf("render after the adoption returned a tool error:\n%s", text)
	}
}
