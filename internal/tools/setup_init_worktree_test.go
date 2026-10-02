// Package tools: tests for setup_init's content/state root split (Task 13,
// R5 D6): git-tracked files (config.toml, both .gitignore blocks,
// plan-template.md, pr-template.md) are written under the active git
// worktree, while gitignored state (local.toml, runs/) stays under the
// main worktree.
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

// dirExists reports whether path exists and is a directory (scaffoldFileExists
// only matches regular files, so runs/ needs its own check).
func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// callRegisteredSetupInitIn runs the registered setup_init tool from dir,
// so root resolution (worktree.MainRoot/ActiveRoot) runs exactly as in
// production. It returns the result and its rendered text.
func callRegisteredSetupInitIn(t *testing.T, dir string, args map[string]any) (*mcp.CallToolResult, string) {
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

	res, err := c.CallTool(ctx, &mcp.CallToolParams{Name: "setup_init", Arguments: args})
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

// TestSetupInit_LinkedWorktree_SplitsRoots verifies the contentRoot/
// stateRoot split: run from a linked worktree, setup_init({}) writes
// config.toml and both .gitignore managed blocks under the linked
// worktree, while local.toml and runs/ land under the main worktree. The
// reported root matches the linked worktree.
func TestSetupInit_LinkedWorktree_SplitsRoots(t *testing.T) {
	mainDir, linkedDir := scaffoldWorktreeFixture(t)

	resolvedLinked, err := filepath.EvalSymlinks(linkedDir)
	if err != nil {
		t.Fatal(err)
	}

	res, text := callRegisteredSetupInitIn(t, linkedDir, map[string]any{})
	if res.IsError {
		t.Fatalf("setup_init from a linked worktree returned an error:\n%s", text)
	}
	if !strings.Contains(text, "- root: "+resolvedLinked) {
		t.Errorf("output must report root=%s:\n%s", resolvedLinked, text)
	}

	if !scaffoldFileExists(filepath.Join(linkedDir, ".sdlc-v2", "config.toml")) {
		t.Error("config.toml not written under the linked worktree")
	}
	if scaffoldFileExists(filepath.Join(mainDir, ".sdlc-v2", "config.toml")) {
		t.Error("config.toml must not be written under the main worktree")
	}

	if !scaffoldFileExists(filepath.Join(linkedDir, ".sdlc-v2", ".gitignore")) {
		t.Error(".sdlc-v2/.gitignore not written under the linked worktree")
	}
	if scaffoldFileExists(filepath.Join(mainDir, ".sdlc-v2", ".gitignore")) {
		t.Error(".sdlc-v2/.gitignore must not be written under the main worktree")
	}

	if !scaffoldFileExists(filepath.Join(linkedDir, ".gitignore")) {
		t.Error("root .gitignore not written under the linked worktree")
	}
	if scaffoldFileExists(filepath.Join(mainDir, ".gitignore")) {
		t.Error("root .gitignore must not be written under the main worktree")
	}

	if !scaffoldFileExists(filepath.Join(mainDir, ".sdlc-v2", "local.toml")) {
		t.Error("local.toml not written under the main worktree")
	}
	if scaffoldFileExists(filepath.Join(linkedDir, ".sdlc-v2", "local.toml")) {
		t.Error("local.toml must not be written under the linked worktree")
	}

	if !dirExists(filepath.Join(mainDir, ".sdlc-v2", "runs")) {
		t.Error(".sdlc-v2/runs/ not created under the main worktree")
	}
	if dirExists(filepath.Join(linkedDir, ".sdlc-v2", "runs")) {
		t.Error(".sdlc-v2/runs/ must not be created under the linked worktree")
	}
}

// TestSetupInit_LinkedWorktree_WritePRTemplate verifies that
// writePRTemplate:true writes pr-template.md under the linked worktree
// (not the main one), and that checkPRTemplate:true from the same
// worktree then reports it as existing.
func TestSetupInit_LinkedWorktree_WritePRTemplate(t *testing.T) {
	_, linkedDir := scaffoldWorktreeFixture(t)

	res, text := callRegisteredSetupInitIn(t, linkedDir, map[string]any{
		"writePRTemplate": true,
		"content":         "x",
	})
	if res.IsError {
		t.Fatalf("writePRTemplate from a linked worktree returned an error:\n%s", text)
	}
	if !scaffoldFileExists(filepath.Join(linkedDir, ".sdlc-v2", "pr-template.md")) {
		t.Error("pr-template.md not written under the linked worktree")
	}

	res, text = callRegisteredSetupInitIn(t, linkedDir, map[string]any{"checkPRTemplate": true})
	if res.IsError {
		t.Fatalf("checkPRTemplate from a linked worktree returned an error:\n%s", text)
	}
	if !strings.Contains(text, "- exists: true") {
		t.Errorf("checkPRTemplate must report exists=true:\n%s", text)
	}
}

// TestSetupInit_NonGitDir_ActiveRootFallsBackToStateRoot covers the
// handler's ActiveRoot()-fails branch: from a non-git temp directory,
// config.toml and local.toml both land under that directory, and the
// reported root is that same directory.
func TestSetupInit_NonGitDir_ActiveRootFallsBackToStateRoot(t *testing.T) {
	dir := t.TempDir()

	res, text := callRegisteredSetupInitIn(t, dir, map[string]any{})
	if res.IsError {
		t.Fatalf("setup_init from a non-git dir returned an error:\n%s", text)
	}

	// The os.Getwd() fallback (no git repo found) reports the directory
	// exactly as t.Chdir received it, unlike worktree.MainRoot/ActiveRoot
	// (via `git rev-parse`), which resolve symlinks. No EvalSymlinks here.
	if !strings.Contains(text, "- root: "+dir) {
		t.Errorf("output must report root=%s:\n%s", dir, text)
	}

	if !scaffoldFileExists(filepath.Join(dir, ".sdlc-v2", "config.toml")) {
		t.Error("config.toml not written under the non-git dir")
	}
	if !scaffoldFileExists(filepath.Join(dir, ".sdlc-v2", "local.toml")) {
		t.Error("local.toml not written under the non-git dir")
	}
}
