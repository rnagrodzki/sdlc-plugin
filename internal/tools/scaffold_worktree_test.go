// Package tools: tests proving scaffold_ci, validate's ci_script_drift
// action, and setup_prepare's ciScriptDrift all read/write the ACTIVE git
// worktree, not the main one (Task 8, R5).
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

// callRegisteredScaffoldCI runs the registered scaffold_ci tool over an
// in-memory MCP session from dir, so root resolution runs exactly as in
// production. GIT_CEILING_DIRECTORIES stops git from walking up past dir's
// parent, so a non-git dir reliably fails root resolution instead of
// accidentally finding this repo's own .git above the temp directory.
func callRegisteredScaffoldCI(t *testing.T, dir string, args map[string]any) (*mcp.CallToolResult, string) {
	t.Helper()
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(dir))
	t.Chdir(dir)

	s := mcpserver.New("test", "0.0.0-test")
	RegisterScaffoldTools(s)
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

	res, err := c.CallTool(ctx, &mcp.CallToolParams{Name: "scaffold_ci", Arguments: args})
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

// callRegisteredSetupPrepare runs the registered setup_prepare tool from a
// fresh non-git temp directory, so the handler's root falls back to cwd and
// its driftRoot falls back to root (ActiveRoot fails too). It returns the
// result, the rendered text, and the directory used.
func callRegisteredSetupPrepare(t *testing.T, args map[string]any) (*mcp.CallToolResult, string, string) {
	t.Helper()
	dir := t.TempDir()
	res, text := callRegisteredSetupPrepareIn(t, dir, args)
	return res, text, dir
}

// callRegisteredSetupPrepareIn is callRegisteredSetupPrepare run from a
// caller-made (and possibly pre-seeded, possibly non-git) directory.
func callRegisteredSetupPrepareIn(t *testing.T, dir string, args map[string]any) (*mcp.CallToolResult, string) {
	t.Helper()
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(dir))
	t.Chdir(dir)

	s := mcpserver.New("test", "0.0.0-test")
	RegisterSetupTools(s)
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

	res, err := c.CallTool(ctx, &mcp.CallToolParams{Name: "setup_prepare", Arguments: args})
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

// scaffoldWorktreeFixture builds a main repo with one commit and a linked
// worktree checked out from it, both initially empty of scaffolded files.
func scaffoldWorktreeFixture(t *testing.T) (mainDir, linkedDir string) {
	t.Helper()
	base := t.TempDir()
	mainDir = filepath.Join(base, "main")
	linkedDir = filepath.Join(base, "linked")
	if err := os.MkdirAll(mainDir, 0o755); err != nil {
		t.Fatal(err)
	}
	initGitFixture(t, mainDir)
	gitCommit(t, mainDir, "initial")
	runGit(t, mainDir, "worktree", "add", "-b", "feat/scaffold-worktree", linkedDir)
	return mainDir, linkedDir
}

// TestScaffoldCI_LinkedWorktree_WritesToLinkedRoot proves scaffold_ci's
// registered handler resolves the ACTIVE worktree, not the main one: run
// from a linked worktree, the 8 manifest files land under the linked tree
// and the main tree's .github/ stays untouched.
func TestScaffoldCI_LinkedWorktree_WritesToLinkedRoot(t *testing.T) {
	mainDir, linkedDir := scaffoldWorktreeFixture(t)

	resolvedLinked, err := filepath.EvalSymlinks(linkedDir)
	if err != nil {
		t.Fatal(err)
	}

	res, text := callRegisteredScaffoldCI(t, linkedDir, map[string]any{})
	if res.IsError {
		t.Fatalf("scaffold_ci from a linked worktree returned an error:\n%s", text)
	}
	if !strings.Contains(text, "- root: "+resolvedLinked) {
		t.Errorf("scaffold_ci output must report root=%s:\n%s", resolvedLinked, text)
	}

	for _, entry := range scaffoldManifest {
		if !scaffoldFileExists(filepath.Join(linkedDir, entry.Dest)) {
			t.Errorf("file %s: not written under the linked worktree", entry.Dest)
		}
		if scaffoldFileExists(filepath.Join(mainDir, entry.Dest)) {
			t.Errorf("file %s: must not be written under the main worktree", entry.Dest)
		}
	}
}

// TestScaffoldCI_RegisteredHandler_NonGitDir_InfraError proves scaffold_ci
// has no cwd fallback (unlike setup_prepare/setup_init): from a directory
// with no git repository, root resolution fails and no file is written.
func TestScaffoldCI_RegisteredHandler_NonGitDir_InfraError(t *testing.T) {
	dir := t.TempDir()

	res, text := callRegisteredScaffoldCI(t, dir, map[string]any{})
	if !res.IsError {
		t.Fatalf("scaffold_ci from a non-git directory must be an error result:\n%s", text)
	}
	if !strings.Contains(text, "resolve project root") {
		t.Errorf("error must name root resolution failure:\n%s", text)
	}
	if !strings.Contains(text, "git rev-parse --show-toplevel") {
		t.Errorf("error suggestion must mention `git rev-parse --show-toplevel`:\n%s", text)
	}

	for _, entry := range scaffoldManifest {
		if scaffoldFileExists(filepath.Join(dir, entry.Dest)) {
			t.Errorf("file %s: must not be written when root resolution fails", entry.Dest)
		}
	}
}

// TestSetupPrepare_RegisteredHandler_NonGitDir_CIScriptDriftAllMissing
// proves the handler's ActiveRoot()-fails branch: unlike scaffold_ci,
// setup_prepare must still succeed from a non-git directory (falling back
// to cwd for both root and driftRoot), reporting every CI script as
// missing.
func TestSetupPrepare_RegisteredHandler_NonGitDir_CIScriptDriftAllMissing(t *testing.T) {
	res, text, _ := callRegisteredSetupPrepare(t, map[string]any{})
	if res.IsError {
		t.Fatalf("setup_prepare from a non-git directory must succeed via the cwd fallback:\n%s", text)
	}

	want := len(scaffoldManifest)
	if got := strings.Count(text, "- action: missing"); got != want {
		t.Errorf("ciScriptDrift action=missing count = %d, want %d (every script unscaffolded):\n%s", got, want, text)
	}
	if strings.Contains(text, "- action: current") || strings.Contains(text, "- action: outdated") {
		t.Errorf("no script should be current/outdated in a bare, unscaffolded directory:\n%s", text)
	}
}

// TestCIScriptDrift_LinkedWorktree_CurrentAfterScaffold_MainStillMissing is
// the end-to-end proof for R5: scaffold_ci writes to the linked worktree,
// so validate's ci_script_drift action and setup_prepare's ciScriptDrift
// must both read that same linked worktree (no findings, every entry
// current) — while the main worktree, untouched by the scaffold, still
// reports every entry missing.
func TestCIScriptDrift_LinkedWorktree_CurrentAfterScaffold_MainStillMissing(t *testing.T) {
	mainDir, linkedDir := scaffoldWorktreeFixture(t)

	if _, err := scaffoldCI(linkedDir, false); err != nil {
		t.Fatalf("scaffoldCI(linkedDir): %v", err)
	}

	t.Chdir(linkedDir)
	if res, text := callRegisteredValidate(t, map[string]any{"action": "ci_script_drift"}); res.IsError {
		t.Fatalf("ci_script_drift from the linked worktree (where scaffold_ci wrote) returned an error:\n%s", text)
	} else if strings.Contains(text, "findings[0]") {
		t.Errorf("ci_script_drift from the linked worktree must report no findings:\n%s", text)
	}

	want := len(scaffoldManifest)

	resLinked, textLinked := callRegisteredSetupPrepareIn(t, linkedDir, map[string]any{})
	if resLinked.IsError {
		t.Fatalf("setup_prepare from the linked worktree returned an error:\n%s", textLinked)
	}
	if got := strings.Count(textLinked, "- action: current"); got != want {
		t.Errorf("linked worktree ciScriptDrift action=current count = %d, want %d:\n%s", got, want, textLinked)
	}
	if strings.Contains(textLinked, "- action: missing") || strings.Contains(textLinked, "- action: outdated") {
		t.Errorf("linked worktree must report no missing/outdated scripts after scaffolding:\n%s", textLinked)
	}

	resMain, textMain := callRegisteredSetupPrepareIn(t, mainDir, map[string]any{})
	if resMain.IsError {
		t.Fatalf("setup_prepare from the main worktree returned an error:\n%s", textMain)
	}
	if got := strings.Count(textMain, "- action: missing"); got != want {
		t.Errorf("main worktree ciScriptDrift action=missing count = %d, want %d (main checkout has no scaffolded files):\n%s", got, want, textMain)
	}
	if strings.Contains(textMain, "- action: current") || strings.Contains(textMain, "- action: outdated") {
		t.Errorf("main worktree must not report current/outdated scripts; it was never scaffolded:\n%s", textMain)
	}
}
