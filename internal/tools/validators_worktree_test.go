package tools

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
)

// callRegisteredValidate calls the registered validate tool over an
// in-memory MCP session, so root resolution runs exactly as in production.
func callRegisteredValidate(t *testing.T, args map[string]any) (*mcp.CallToolResult, string) {
	t.Helper()
	s := mcpserver.New("test", "0.0.0-test")
	RegisterValidateTools(s)

	ctx := context.Background()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	if _, err := s.MCPServer().Connect(ctx, serverTransport, nil); err != nil {
		t.Fatalf("server Connect: %v", err)
	}
	c, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0.0.0"}, nil).Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client Connect: %v", err)
	}
	t.Cleanup(func() { c.Close() })

	res, err := c.CallTool(ctx, &mcp.CallToolParams{Name: "validate", Arguments: args})
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

// validateWorktreeFixture builds a main repo whose config has one valid
// guardrail and a linked worktree whose config adds an invalid one, so a
// guardrails check reports a finding only when it reads the linked tree.
func validateWorktreeFixture(t *testing.T) (mainDir, linkedDir string) {
	t.Helper()
	base := t.TempDir()
	mainDir = filepath.Join(base, "main")
	linkedDir = filepath.Join(base, "linked")
	if err := os.MkdirAll(mainDir, 0o755); err != nil {
		t.Fatal(err)
	}
	initGitFixture(t, mainDir)
	gitCommit(t, mainDir, "initial")
	runGit(t, mainDir, "worktree", "add", "-b", "feat/linked", linkedDir)

	const good = "[plan.guardrails.good-guardrail]\ndescription = \"A valid guardrail description.\"\n"
	writeFile(t, filepath.Join(mainDir, paths.DataDir, "config.toml"), good)
	writeFile(t, filepath.Join(linkedDir, paths.DataDir, "config.toml"),
		good+"\n[plan.guardrails.Bad_ID]\ndescription = \"desc\"\n")
	return mainDir, linkedDir
}

// TestValidate_GuardrailsActiveWorktree_ReadsLinkedTree proves the root swap
// in the registered handler: from a linked worktree, activeWorktree:true
// reads that tree's config (one finding), and the default reads the main
// tree's config (no findings).
func TestValidate_GuardrailsActiveWorktree_ReadsLinkedTree(t *testing.T) {
	_, linkedDir := validateWorktreeFixture(t)
	t.Chdir(linkedDir)

	res, text := callRegisteredValidate(t, map[string]any{"action": "guardrails", "activeWorktree": true})
	if res.IsError {
		t.Fatalf("activeWorktree:true returned an error:\n%s", text)
	}
	if !strings.Contains(text, "Bad_ID") {
		t.Errorf("activeWorktree:true must report the linked tree's Bad_ID guardrail:\n%s", text)
	}

	res, text = callRegisteredValidate(t, map[string]any{"action": "guardrails"})
	if res.IsError {
		t.Fatalf("default guardrails returned an error:\n%s", text)
	}
	if strings.Contains(text, "Bad_ID") {
		t.Errorf("default guardrails must read the main tree, which has no Bad_ID:\n%s", text)
	}
}

// TestValidate_GuardrailsActiveWorktree_UnresolvableFailsLoud runs from the
// main repo's .git directory, where the main root resolves but the active
// root does not. The opt-in guardrails check must fail, not silently read
// the main tree; dimensions keeps its fail-open fallback.
func TestValidate_GuardrailsActiveWorktree_UnresolvableFailsLoud(t *testing.T) {
	mainDir, _ := validateWorktreeFixture(t)
	t.Chdir(filepath.Join(mainDir, ".git"))

	res, text := callRegisteredValidate(t, map[string]any{"action": "guardrails", "activeWorktree": true})
	if !res.IsError {
		t.Fatalf("activeWorktree:true with no active worktree must be an error result:\n%s", text)
	}
	if !strings.Contains(text, "resolve active worktree") {
		t.Errorf("error must name the active-worktree resolution failure:\n%s", text)
	}

	if res, text := callRegisteredValidate(t, map[string]any{"action": "dimensions"}); res.IsError {
		t.Errorf("dimensions must fall back to the main root, got an error:\n%s", text)
	}
}

func TestValidateRoot(t *testing.T) {
	boom := errors.New("boom")
	ok := func() (string, error) { return "/active", nil }
	fail := func() (string, error) { return "", boom }

	cases := []struct {
		name    string
		in      ValidateIn
		active  func() (string, error)
		want    string
		wantErr bool
	}{
		{"guardrails default reads main", ValidateIn{Action: "guardrails"}, ok, "/main", false},
		{"guardrails opt-in reads active", ValidateIn{Action: "guardrails", ActiveWorktree: true}, ok, "/active", false},
		{"guardrails opt-in fails loud", ValidateIn{Action: "guardrails", ActiveWorktree: true}, fail, "", true},
		{"dimensions reads active", ValidateIn{Action: "dimensions"}, ok, "/active", false},
		{"dimensions fails open", ValidateIn{Action: "dimensions"}, fail, "/main", false},
		{"ci_script_drift reads active", ValidateIn{Action: "ci_script_drift"}, ok, "/active", false},
		{"ci_script_drift fails open", ValidateIn{Action: "ci_script_drift"}, fail, "/main", false},
		{"other action ignores the flag", ValidateIn{Action: "discovery", ActiveWorktree: true}, fail, "/main", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := validateRoot("/main", tc.in, tc.active)
			if tc.wantErr {
				var infra *mcpserver.InfraError
				if !errors.As(err, &infra) || !errors.Is(err, boom) {
					t.Fatalf("err = %v, want an InfraError wrapping the resolution error", err)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Errorf("validateRoot = (%q, %v), want (%q, nil)", got, err, tc.want)
			}
		})
	}
}
