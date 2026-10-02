// Package tools: tests for setup_init's content/state root split (Task 13,
// R5 D6): git-tracked files (config.toml, both .gitignore blocks,
// plan-template.md, pr-template.md) are written under the active git
// worktree, while gitignored state (local.toml, runs/) stays under the
// main worktree.
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

// TestSetupInitRoots_ModeBranches_UseContentRoot calls setupInitRoots with
// contentRoot != stateRoot and checks each of the five mode-select branches
// reports out.Root == contentRoot and reads or writes its template under
// contentRoot only. The check/read templates are seeded under contentRoot
// alone, so a branch wired to stateRoot would report Exists=false.
func TestSetupInitRoots_ModeBranches_UseContentRoot(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_PLUGIN_ROOT", filepath.Join("testdata", "plugins", "sdlc"))
	resetSkillTemplateIndex()
	t.Cleanup(resetSkillTemplateIndex)

	cases := []struct {
		name       string
		in         SetupInitIn
		seed       string // template seeded under contentRoot before the call
		written    string // template the call must write under contentRoot
		wantExists bool
	}{
		{name: "WritePlanTemplate", in: SetupInitIn{WritePlanTemplate: true}, written: "plan-template.md"},
		{name: "WritePRTemplate", in: SetupInitIn{WritePRTemplate: true, Content: "x"}, written: "pr-template.md"},
		{name: "CheckPlanTemplate", in: SetupInitIn{CheckPlanTemplate: true}, seed: "plan-template.md", wantExists: true},
		{name: "CheckPRTemplate", in: SetupInitIn{CheckPRTemplate: true}, seed: "pr-template.md", wantExists: true},
		{name: "ReadPlanTemplate", in: SetupInitIn{ReadPlanTemplate: true}, seed: "plan-template.md", wantExists: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			contentRoot, stateRoot := t.TempDir(), t.TempDir()
			if tc.seed != "" {
				dir := filepath.Join(contentRoot, paths.DataDir)
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, tc.seed), []byte("# T\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}

			out, err := setupInitRoots(contentRoot, stateRoot, tc.in)
			if err != nil {
				t.Fatalf("setupInitRoots: %v", err)
			}
			if out.Root != contentRoot {
				t.Errorf("Root = %q, want contentRoot %q (stateRoot is %q)", out.Root, contentRoot, stateRoot)
			}
			if out.Exists != tc.wantExists {
				t.Errorf("Exists = %v, want %v", out.Exists, tc.wantExists)
			}
			if tc.written != "" {
				if !scaffoldFileExists(filepath.Join(contentRoot, paths.DataDir, tc.written)) {
					t.Errorf("%s not written under contentRoot", tc.written)
				}
				if scaffoldFileExists(filepath.Join(stateRoot, paths.DataDir, tc.written)) {
					t.Errorf("%s must not be written under stateRoot", tc.written)
				}
			}
			if dirExists(filepath.Join(stateRoot, paths.DataDir)) {
				t.Error("a mode-select call must not touch stateRoot")
			}
		})
	}
}

// TestSetupInitRoots_LegacyJSONMigration_SplitRoots seeds a legacy
// config.json under contentRoot and a legacy local.json under stateRoot,
// plus a decoy of each under the other root. Each legacy file must be
// renamed to .bak under its own root, and the decoys must stay untouched,
// so a loop that ran either entry against the wrong root fails here.
func TestSetupInitRoots_LegacyJSONMigration_SplitRoots(t *testing.T) {
	contentRoot, stateRoot := t.TempDir(), t.TempDir()
	contentDir := filepath.Join(contentRoot, paths.DataDir)
	stateDir := filepath.Join(stateRoot, paths.DataDir)
	seed := map[string]string{
		filepath.Join(contentDir, "config.json"): `{"old":"config"}`,
		filepath.Join(stateDir, "local.json"):    `{"old":"local"}`,
		filepath.Join(contentDir, "local.json"):  `{"decoy":"local"}`,
		filepath.Join(stateDir, "config.json"):   `{"decoy":"config"}`,
	}
	for p, body := range seed {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	out, err := setupInitRoots(contentRoot, stateRoot, SetupInitIn{})
	if err != nil {
		t.Fatalf("setupInitRoots: %v", err)
	}
	if !out.OK {
		t.Errorf("expected OK=true, errors: %v", out.Errors)
	}

	wantBak := map[string]string{
		filepath.Join(contentDir, "config.json.bak"): `{"old":"config"}`,
		filepath.Join(stateDir, "local.json.bak"):    `{"old":"local"}`,
	}
	for p, body := range wantBak {
		got, err := os.ReadFile(p)
		if err != nil {
			t.Errorf("%s should exist: %v", p, err)
			continue
		}
		if string(got) != body {
			t.Errorf("%s = %q, want %q", p, got, body)
		}
	}
	for _, p := range []string{filepath.Join(contentDir, "config.json"), filepath.Join(stateDir, "local.json")} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s should be renamed away", p)
		}
	}

	// Decoys: config.json under stateRoot and local.json under contentRoot
	// are not migration targets and must be left exactly as seeded.
	for _, p := range []string{filepath.Join(stateDir, "config.json"), filepath.Join(contentDir, "local.json")} {
		got, err := os.ReadFile(p)
		if err != nil {
			t.Errorf("decoy %s must not be renamed: %v", p, err)
			continue
		}
		if string(got) != seed[p] {
			t.Errorf("decoy %s = %q, want %q", p, got, seed[p])
		}
	}
	for _, p := range []string{filepath.Join(stateDir, "config.json.bak"), filepath.Join(contentDir, "local.json.bak")} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s must not be created", p)
		}
	}
}

// TestSetupInitRoots_StateRootMkdirFails makes stateRoot read-only so the
// first MkdirAll (contentRoot/.sdlc-v2) succeeds and the second
// (stateRoot/.sdlc-v2) fails. The call must return an InfraError before
// writing any file: contentRoot/.sdlc-v2 may exist but must stay empty.
// After permissions are restored, a re-run must complete normally.
func TestSetupInitRoots_StateRootMkdirFails(t *testing.T) {
	contentRoot, stateRoot := t.TempDir(), t.TempDir()
	shipErrChmod(t, stateRoot, 0o555)

	_, err := setupInitRoots(contentRoot, stateRoot, SetupInitIn{})
	if err == nil {
		t.Fatal("expected an error when stateRoot/.sdlc-v2 cannot be created")
	}
	var infra *mcpserver.InfraError
	if !errors.As(err, &infra) {
		t.Fatalf("expected *mcpserver.InfraError, got %T: %v", err, err)
	}
	requireShipPermissionCause(t, infra.Cause)

	entries, readErr := os.ReadDir(filepath.Join(contentRoot, paths.DataDir))
	if readErr != nil && !os.IsNotExist(readErr) {
		t.Fatalf("read contentRoot/%s: %v", paths.DataDir, readErr)
	}
	if len(entries) != 0 {
		t.Errorf("contentRoot/%s must stay empty after the failure, got %d entries", paths.DataDir, len(entries))
	}
	if scaffoldFileExists(filepath.Join(contentRoot, ".gitignore")) {
		t.Error("root .gitignore must not be written after the failure")
	}

	if err := os.Chmod(stateRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := setupInitRoots(contentRoot, stateRoot, SetupInitIn{})
	if err != nil {
		t.Fatalf("re-run after restoring permissions: %v", err)
	}
	if !out.OK {
		t.Errorf("re-run expected OK=true, errors: %v", out.Errors)
	}
	if !scaffoldFileExists(filepath.Join(contentRoot, paths.DataDir, paths.ConfigFile)) {
		t.Error("config.toml not written under contentRoot on re-run")
	}
	if !scaffoldFileExists(filepath.Join(stateRoot, paths.DataDir, paths.LocalConfigFile)) {
		t.Error("local.toml not written under stateRoot on re-run")
	}
}
