// Package tools: tests for setup_write_sections's scaffold_ci auto-trigger
// (Task 10 addition).
package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
)

// callRegisteredSetupWriteSections runs the registered setup_write_sections
// tool over an in-memory MCP session from a fresh non-git directory, so the
// handler falls back to that directory as the project root. It returns the
// result and that directory.
func callRegisteredSetupWriteSections(t *testing.T, sectionsJSON string) (*mcp.CallToolResult, string, string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(dir))
	t.Chdir(dir)

	s := mcpserver.New("test", "0.0.0-test")
	RegisterSetupWriteTools(s)
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

	res, err := c.CallTool(ctx, &mcp.CallToolParams{
		Name:      "setup_write_sections",
		Arguments: map[string]any{"sectionsJson": sectionsJSON},
	})
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
	return res, text.Text, dir
}

// TestSetupWriteSections_UnknownTopLevelKeyRejected verifies that a section
// key whose first segment is neither a project nor a local section (e.g. a
// typo) is rejected with a DomainError naming the key and the allowed keys,
// and that nothing is written — not even the valid keys in the same call.
func TestSetupWriteSections_UnknownTopLevelKeyRejected(t *testing.T) {
	cases := map[string]struct {
		sectionsJSON string
		unknown      string
	}{
		"typo of a local key":        {`{"shp":{"draft":true}}`, "shp"},
		"dotted unknown key":         {`{"foo.bar":{"x":1}}`, "foo.bar"},
		"unknown next to valid keys": {`{"commit":{"style":"conventional"},"ship":{"draft":true},"reviw":{"x":1}}`, "reviw"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			res, text, dir := callRegisteredSetupWriteSections(t, tc.sectionsJSON)
			if !res.IsError {
				t.Fatalf("expected a tool error, got success:\n%s", text)
			}
			for _, want := range []string{"setup_write_sections: unknown section keys", tc.unknown, "ship", "automation", "commit"} {
				if !strings.Contains(text, want) {
					t.Errorf("error text missing %q:\n%s", want, text)
				}
			}
			for _, f := range []string{"config.toml", "local.toml"} {
				if _, err := os.Stat(filepath.Join(dir, ".sdlc-v2", f)); !os.IsNotExist(err) {
					t.Errorf(".sdlc-v2/%s was written (stat err: %v); want no write", f, err)
				}
			}
		})
	}
}

// TestSetupWriteSections_KnownLocalKeyWritten verifies that a known local
// section key still routes to .sdlc-v2/local.toml.
func TestSetupWriteSections_KnownLocalKeyWritten(t *testing.T) {
	res, text, dir := callRegisteredSetupWriteSections(t, `{"planStyle":{"style":"compact"}}`)
	if res.IsError {
		t.Fatalf("expected success, got tool error:\n%s", text)
	}
	data, err := os.ReadFile(filepath.Join(dir, ".sdlc-v2", "local.toml"))
	if err != nil {
		t.Fatalf("read local.toml: %v", err)
	}
	if !strings.Contains(string(data), "planStyle") {
		t.Errorf("local.toml has no planStyle table:\n%s", data)
	}
	if _, err := os.Stat(filepath.Join(dir, ".sdlc-v2", "config.toml")); !os.IsNotExist(err) {
		t.Errorf("config.toml was written for a local key (stat err: %v)", err)
	}
}

// TestSetupWriteSections_VersionTriggersScaffold verifies that writing a
// "version" section auto-triggers scaffold_ci and reports the resulting
// file actions on the output.
func TestSetupWriteSections_VersionTriggersScaffold(t *testing.T) {
	root := t.TempDir()

	out, err := setupWriteSections(root, SetupWriteSectionsIn{
		SectionsJSON: `{"version":{"tag.enabled":true,"tag.prefix":"v"}}`,
	})
	if err != nil {
		t.Fatalf("setupWriteSections: %v", err)
	}
	if !out.OK {
		t.Fatalf("expected OK=true, got errors: %v", out.Errors)
	}
	if len(out.Written) != 1 || out.Written[0] != "version" {
		t.Fatalf("expected written=[version], got %v", out.Written)
	}

	if len(out.Scaffold) != 10 {
		t.Fatalf("expected 10 scaffold file reports, got %d", len(out.Scaffold))
	}
	for _, f := range out.Scaffold {
		if f.Action != "created" {
			t.Errorf("file %s: expected action 'created', got %q", f.Path, f.Action)
		}
	}

	destPath := filepath.Join(root, ".github", "workflows", "release-on-main.yml")
	if !scaffoldFileExists(destPath) {
		t.Errorf("release-on-main.yml not written to disk at %s", destPath)
	}
}

// TestSetupWriteSections_NonVersionSkipsScaffold verifies that writing a
// section batch without "version" does not trigger scaffold_ci.
func TestSetupWriteSections_NonVersionSkipsScaffold(t *testing.T) {
	root := t.TempDir()

	out, err := setupWriteSections(root, SetupWriteSectionsIn{
		SectionsJSON: `{"commit":{"style":"conventional"}}`,
	})
	if err != nil {
		t.Fatalf("setupWriteSections: %v", err)
	}
	if !out.OK {
		t.Fatalf("expected OK=true, got errors: %v", out.Errors)
	}
	if out.Scaffold != nil {
		t.Errorf("expected no scaffold reports, got %v", out.Scaffold)
	}

	destPath := filepath.Join(root, ".github", "workflows", "release-on-main.yml")
	if scaffoldFileExists(destPath) {
		t.Errorf("release-on-main.yml unexpectedly written to disk at %s", destPath)
	}
}

// TestSetupWriteSections_VersionScaffoldIdempotent verifies a second call
// with "version" re-triggers scaffold_ci non-destructively (all skipped,
// no error, config write still succeeds).
func TestSetupWriteSections_VersionScaffoldIdempotent(t *testing.T) {
	root := t.TempDir()

	if _, err := setupWriteSections(root, SetupWriteSectionsIn{
		SectionsJSON: `{"version":{"tag.enabled":true,"tag.prefix":"v"}}`,
	}); err != nil {
		t.Fatalf("setupWriteSections (first): %v", err)
	}

	out, err := setupWriteSections(root, SetupWriteSectionsIn{
		SectionsJSON: `{"version":{"tag.enabled":true,"tag.prefix":"v"}}`,
	})
	if err != nil {
		t.Fatalf("setupWriteSections (second): %v", err)
	}
	if !out.OK {
		t.Fatalf("expected OK=true, got errors: %v", out.Errors)
	}
	for _, f := range out.Scaffold {
		if f.Action != "skipped" {
			t.Errorf("file %s: expected action 'skipped' on second run, got %q", f.Path, f.Action)
		}
	}
}
