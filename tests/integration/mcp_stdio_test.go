//go:build integration

// mcp_stdio_test.go spawns the real, already-built sdlc binary (sdlcBinPath,
// built once by TestMain in pipeline_smoke_test.go) as a genuine OS
// subprocess running "sdlc mcp" and talks to it over its actual stdin/
// stdout using mcp.CommandTransport — the same stdio transport .mcp.json's
// launcher ultimately execs the binary under. This is deliberately not the
// in-process mcp.NewInMemoryTransports() pattern used elsewhere in this
// package and in internal/skillcheck: those exercise tool handlers
// directly against the mcp.Server value; this exercises the whole startup
// path (process start, stdin/stdout pipes, the initialize handshake, and a
// real request/response round trip across the process boundary) — the
// exact thing that was broken when the MCP service was reported down.
package integration

import (
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"sort"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// expectedMCPTools is the full tool surface cmd/sdlc/main.go's runMCP()
// registers. Kept in sync by hand with that function's Register*Tools
// calls — a mismatch here means either this test or runMCP() drifted.
var expectedMCPTools = []string{
	"commit_apply", "commit_prepare", "dimensions_render_instructions",
	"execute_state", "jira", "learnings_log", "links_validate",
	"mcp_failure_record", "migrate", "openspec_enrich",
	"plan_explore_prepare", "plan_mark", "plan_prepare", "plan_support",
	"poll_await", "pr_apply", "pr_prepare", "prepare_orchestrator",
	"received_review_prepare", "review_prepare", "scaffold_ci",
	"setup_init", "setup_prepare", "setup_write_sections", "ship_prepare",
	"ship_state", "ship_verify_side_effect", "validate",
	"verify_pipeline_classify", "verify_tag_ancestry", "version_apply",
	"version_prepare",
}

// connectStdioClient spawns sdlcBinPath as "sdlc mcp" with its working
// directory set to dir, and connects an MCP client to it over stdio.
func connectStdioClient(t *testing.T, dir string) *mcp.ClientSession {
	t.Helper()
	cmd := exec.Command(sdlcBinPath, "mcp")
	cmd.Dir = dir

	client := mcp.NewClient(&mcp.Implementation{Name: "mcp-stdio-test", Version: "0.0.0"}, nil)
	c, err := client.Connect(context.Background(), &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		t.Fatalf("stdio client Connect: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

// TestMCPStdio_StartupAndToolSurface verifies the real subprocess starts,
// completes the MCP initialize handshake, and reports the complete
// expected tool surface — not a subset, not extras.
func TestMCPStdio_StartupAndToolSurface(t *testing.T) {
	c := connectStdioClient(t, t.TempDir())

	resp, err := c.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}

	got := make([]string, 0, len(resp.Tools))
	for _, tl := range resp.Tools {
		got = append(got, tl.Name)
	}
	sort.Strings(got)

	want := append([]string(nil), expectedMCPTools...)
	sort.Strings(want)

	if len(got) != len(want) {
		t.Fatalf("tool count = %d, want %d\ngot:  %v\nwant: %v", len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("tool surface mismatch at index %d: got %q, want %q\ngot:  %v\nwant: %v", i, got[i], want[i], got, want)
		}
	}
}

// TestMCPStdio_CallToolRoundTrip exercises one representative tool call
// through the real subprocess boundary: request marshaled, written to the
// child's stdin, handled, response read back from its stdout, and
// unmarshaled — proving the transport carries real tool traffic, not just
// the initialize handshake.
func TestMCPStdio_CallToolRoundTrip(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init", "-q")
	mustWriteFile(t, filepath.Join(dir, "NOTES.md"), "no links here\n")

	c := connectStdioClient(t, dir)

	env := callTool(t, c, "links_validate", map[string]any{"file": "NOTES.md", "offline": true})
	if !env.OK {
		t.Fatalf("links_validate: not ok: code=%s error=%s", env.Code, env.Error)
	}

	var data struct {
		Results []map[string]any `json:"results"`
	}
	if err := json.Unmarshal(env.Data, &data); err != nil {
		t.Fatalf("unmarshal links_validate data: %v\nraw: %s", err, env.Data)
	}
	if len(data.Results) != 0 {
		t.Fatalf("links_validate results = %v, want empty (no URLs in NOTES.md)", data.Results)
	}
}
