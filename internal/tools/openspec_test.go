package tools

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
)

// callRegisteredOpenspecEnrich writes configYAML to openspec/config.yaml in
// a fresh git repo, runs the registered openspec_enrich tool there over an
// in-memory MCP session, and returns the result text and the file content
// after the call.
func callRegisteredOpenspecEnrich(t *testing.T, configYAML string, args map[string]any) (string, string) {
	t.Helper()
	dir := t.TempDir()
	if out, err := exec.Command("git", "-C", dir, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	path := filepath.Join(dir, "openspec", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(configYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(dir))
	t.Chdir(dir)

	s := mcpserver.New("test", "0.0.0-test")
	RegisterOpenspecTools(s)
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

	if args == nil {
		args = map[string]any{}
	}
	res, err := c.CallTool(ctx, &mcp.CallToolParams{Name: "openspec_enrich", Arguments: args})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected tool error: %+v", res.Content)
	}
	text, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("content[0] is %T, want *mcp.TextContent", res.Content[0])
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return text.Text, string(got)
}

// managedBlock returns a complete managed block at version v with body as
// its context text.
func managedBlock(v, body string) string {
	return "# BEGIN MANAGED BY sdlc-v2 (v" + v + ")\ncontext: |\n  " + body + "\n# END MANAGED BY sdlc-v2 (v" + v + ")"
}

// TestOpenspecEnrich_OlderBlockUpdated verifies the spec'd update path: a
// complete block at v1 with no other top-level context: key is replaced in
// place by the v2 block, and the text before and after it is kept.
func TestOpenspecEnrich_OlderBlockUpdated(t *testing.T) {
	before := "name: test-project\n\n"
	after := "\nrules:\n  proposal: keep\n"
	text, got := callRegisteredOpenspecEnrich(t, before+managedBlock("1", "old text")+after, nil)

	for _, want := range []string{"- action: update\n", "- version: 2\n", "- changed: true\n"} {
		if !strings.Contains(text, want) {
			t.Errorf("result missing %q:\n%s", want, text)
		}
	}
	if want := before + enrichBlockTemplate + after; got != want {
		t.Errorf("file is not the old file with only the block replaced.\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
	if !strings.Contains(got, "# BEGIN MANAGED BY sdlc-v2 (v2)") {
		t.Errorf("block does not start with the v2 begin line:\n%s", got)
	}
}

// TestOpenspecEnrich_NewerBlockLeftAlone verifies the spec'd newer-block
// path: a block above the shipped version is not touched, and the result
// carries that block's version and the downgrade warning.
func TestOpenspecEnrich_NewerBlockLeftAlone(t *testing.T) {
	orig := "name: test-project\n\n" + managedBlock("3", "future text") + "\n"
	text, got := callRegisteredOpenspecEnrich(t, orig, nil)

	for _, want := range []string{
		"- action: unchanged\n",
		"- version: 3\n",
		"- changed: false\n",
		"Managed block is at v3, plugin ships v2. Use --remove to downgrade.",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("result missing %q:\n%s", want, text)
		}
	}
	if got != orig {
		t.Errorf("file changed.\n--- got ---\n%s\n--- want ---\n%s", got, orig)
	}
}

// TestOpenspecEnrich_OlderBlockWithOutsideContextSkipped verifies that an
// older block is not updated when a top-level context: key exists outside
// it: the action is skipped-existing-context, the version is the block's
// own, and the file is unchanged.
func TestOpenspecEnrich_OlderBlockWithOutsideContextSkipped(t *testing.T) {
	orig := "name: test-project\ncontext: hand written\n\n" + managedBlock("1", "old text") + "\n"
	text, got := callRegisteredOpenspecEnrich(t, orig, nil)

	for _, want := range []string{
		"- action: skipped-existing-context\n",
		"- version: 1\n",
		"- changed: false\n",
		"Top-level context: key already present outside the managed block in openspec/config.yaml. Refusing to update — a duplicate context: key would result. Manually fold the sdlc-v2 workflow guidance into your existing context: value, then re-run --openspec-enrich.",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("result missing %q:\n%s", want, text)
		}
	}
	if got != orig {
		t.Errorf("file changed.\n--- got ---\n%s\n--- want ---\n%s", got, orig)
	}
}

// TestOpenspecEnrich_ContextWithoutBlockSkipped verifies the no-block
// variant of skipped-existing-context and its exact warning text.
func TestOpenspecEnrich_ContextWithoutBlockSkipped(t *testing.T) {
	orig := "name: test-project\ncontext: hand written\n"
	text, got := callRegisteredOpenspecEnrich(t, orig, nil)

	for _, want := range []string{
		"- action: skipped-existing-context\n",
		"- version: 2\n",
		"Top-level context: key already present in openspec/config.yaml. Refusing to inject a duplicate. Manually fold the sdlc-v2 workflow guidance into your existing context: value, then re-run --openspec-enrich.",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("result missing %q:\n%s", want, text)
		}
	}
	if got != orig {
		t.Errorf("file changed.\n--- got ---\n%s\n--- want ---\n%s", got, orig)
	}
}
