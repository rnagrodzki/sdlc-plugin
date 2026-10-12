//go:build integration

package tools

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
)

// callRegisteredPrepareOrchestratorText calls the registered
// prepare_orchestrator tool over an in-memory MCP session and returns the
// result with the whole text of its first content block, which is the Markdown
// a caller reads.
func callRegisteredPrepareOrchestratorText(t *testing.T, args map[string]any) (*mcp.CallToolResult, string) {
	t.Helper()
	s := mcpserver.New("test", "0.0.0-test")
	RegisterPrepareOrchestratorTools(s)

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

	res, err := c.CallTool(ctx, &mcp.CallToolParams{Name: "prepare_orchestrator", Arguments: args})
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

// manifestPathFromText returns the manifestPath bullet of a rendered
// prepare_orchestrator result. It fails the test when the bullet is missing.
func manifestPathFromText(t *testing.T, text string) string {
	t.Helper()
	const prefix = "- manifestPath: "
	for _, line := range strings.Split(text, "\n") {
		if path, ok := strings.CutPrefix(line, prefix); ok {
			return path
		}
	}
	t.Fatalf("output has no %q bullet:\n%s", prefix, text)
	return ""
}

// TestIntegration_PrepareOrchestratorHarden_WritesPreflightFindings calls the
// registered prepare_orchestrator tool in harden mode on a repository whose
// config holds one guardrail with a 1474-byte description. The call must not
// stop. The manifest on disk holds one preflightFindings entry with bytesOver
// 450, and the output carries the count 1 and the next sentence for findings.
func TestIntegration_PrepareOrchestratorHarden_WritesPreflightFindings(t *testing.T) {
	tmpDir := redirectTempDir(t)
	newOrchestratorRepo(t, ""+
		"[execute.guardrails.long-rule]\n"+
		"description = \""+strings.Repeat("a", 1474)+"\"\n")

	res, text := callRegisteredPrepareOrchestratorText(t, map[string]any{
		"mode":            "harden",
		"skill":           "ship",
		"failureText":     "boom",
		"skipConfigCheck": true,
	})
	if res.IsError {
		t.Fatalf("prepare_orchestrator returned a tool error:\n%s", text)
	}

	manifestPath := manifestPathFromText(t, text)
	if !strings.HasPrefix(manifestPath, tmpDir+string(filepath.Separator)) {
		t.Errorf("manifestPath %q is not under the redirected temp dir %q", manifestPath, tmpDir)
	}
	findings := manifestPreflightFindings(t, manifestPath)
	if len(findings) != 1 {
		t.Fatalf("manifest preflightFindings = %+v, want 1 entry", findings)
	}
	f := findings[0]
	if f["surface"] != "execute-guardrails" || f["id"] != "long-rule" || f["severity"] != "error" || f["bytesOver"] != float64(450) {
		t.Errorf("unexpected manifest finding: %+v", f)
	}

	wantNext := prepareNextHarden + " 1 pre-flight findings: the harden orchestrator drafts repair proposals for them first."
	for _, want := range []string{"- preflightFindingCount: 1", "**Next:** " + wantNext} {
		if !strings.Contains(text, want) {
			t.Errorf("output missing %q:\n%s", want, text)
		}
	}
}

// TestIntegration_PrepareOrchestratorHarden_CleanConfigWritesEmptyList calls
// the registered prepare_orchestrator tool in harden mode on a repository with
// a clean config. The manifest on disk holds preflightFindings as an empty
// array (not null), and the output carries the count 0 and the plain next text.
func TestIntegration_PrepareOrchestratorHarden_CleanConfigWritesEmptyList(t *testing.T) {
	redirectTempDir(t)
	newOrchestratorRepo(t, ""+
		"[execute.guardrails.good-rule]\n"+
		"description = \"Every task names its files.\"\n")

	res, text := callRegisteredPrepareOrchestratorText(t, map[string]any{
		"mode":            "harden",
		"skill":           "ship",
		"failureText":     "boom",
		"skipConfigCheck": true,
	})
	if res.IsError {
		t.Fatalf("prepare_orchestrator returned a tool error:\n%s", text)
	}

	manifest := readHardenManifest(t, manifestPathFromText(t, text))
	list, ok := manifest["preflightFindings"].([]any)
	if !ok || len(list) != 0 {
		t.Fatalf("manifest preflightFindings = %#v, want an empty array (not null)", manifest["preflightFindings"])
	}

	for _, want := range []string{"- preflightFindingCount: 0", "**Next:** " + prepareNextHarden} {
		if !strings.Contains(text, want) {
			t.Errorf("output missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "pre-flight findings") {
		t.Errorf("output must not carry the findings sentence for a clean config:\n%s", text)
	}
}
