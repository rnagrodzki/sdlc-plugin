//go:build integration

// preplan_context_test.go drives the plan_support action preplan_context
// through the real sdlc binary over stdio (connectStdioClient), the same
// boundary mcp_stdio_test.go uses. The action writes a file under
// .sdlc-v2/preplan/, so this test checks both the returned result and the
// file on disk.
package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// preplanWordsSuggestion is the Suggestion text that preplan_context returns
// for a topic with no ASCII letter or digit.
const preplanWordsSuggestion = `Pass a topic name with ASCII letters or digits, for example "auth flow".`

// preplanDirEntries returns the names of the entries in dir, or fails the test
// when dir cannot be read. A missing dir returns an empty list.
func preplanDirEntries(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatalf("read dir %s: %v", dir, err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// TestMCPStdio_PreplanContext calls plan_support with action preplan_context
// through the real subprocess. It asserts that the first call creates the topic
// file with the skeleton and reports it, that a second call leaves an edited
// file unchanged and reports preplanCreated false, and that a bad topic returns
// a domain error with a Suggestion and creates no file.
func TestMCPStdio_PreplanContext(t *testing.T) {
	dir := realPath(t, t.TempDir())
	runGit(t, dir, "init", "-q")
	preplanDir := filepath.Join(dir, ".sdlc-v2", "preplan")
	file := filepath.Join(preplanDir, "auth-flow.md")

	c := connectStdioClient(t, dir)
	args := map[string]any{"action": "preplan_context", "topic": "auth flow"}

	// First call: the file is absent, so the call creates it.
	env := callTool(t, c, "plan_support", args)
	if !env.OK {
		t.Fatalf("preplan_context: not ok: code=%s body=%s", env.Code, env.Body)
	}
	got, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("topic file %s was not created: %v", file, err)
	}
	if !strings.HasPrefix(string(got), "# Preplan: auth flow\n\n**Status:** in progress\n") {
		t.Fatalf("topic file does not start with the skeleton heading:\n%s", got)
	}
	for _, want := range []string{
		"- preplanCreated: true",
		"- preplanFile: " + file,
		"Created topic file auth-flow.md.",
	} {
		if !strings.Contains(env.Body, want) {
			t.Errorf("first call: want %q in body, got:\n%s", want, env.Body)
		}
	}

	// Second call: the user edited the file, so the call must not overwrite it.
	edited := string(got) + "My own note.\n"
	if err := os.WriteFile(file, []byte(edited), 0o644); err != nil {
		t.Fatalf("edit topic file: %v", err)
	}
	env = callTool(t, c, "plan_support", args)
	if !env.OK {
		t.Fatalf("preplan_context (second call): not ok: code=%s body=%s", env.Code, env.Body)
	}
	after, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read topic file after the second call: %v", err)
	}
	if string(after) != edited {
		t.Errorf("second call changed the topic file\nwant:\n%s\ngot:\n%s", edited, after)
	}
	for _, want := range []string{
		"- preplanCreated: false",
		"- preplanFile: " + file,
		"Topic file auth-flow.md exists.",
	} {
		if !strings.Contains(env.Body, want) {
			t.Errorf("second call: want %q in body, got:\n%s", want, env.Body)
		}
	}

	// Bad topic: a domain error with a Suggestion, and no new file.
	env = callTool(t, c, "plan_support", map[string]any{"action": "preplan_context", "topic": "!!!"})
	if env.OK {
		t.Fatalf("preplan_context with topic %q: want an error, got ok:\n%s", "!!!", env.Body)
	}
	if env.Code != "domain" {
		t.Errorf("error code = %q, want domain", env.Code)
	}
	if want := "## Do this\n" + preplanWordsSuggestion; !strings.Contains(env.Body, want) {
		t.Errorf("want the Suggestion %q in body, got:\n%s", preplanWordsSuggestion, env.Body)
	}
	if names := preplanDirEntries(t, preplanDir); len(names) != 1 || names[0] != "auth-flow.md" {
		t.Errorf("preplan dir entries = %v, want only [auth-flow.md]", names)
	}
}
