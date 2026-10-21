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
	"regexp"
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

	c := connectStdioClient(t, dir)
	args := map[string]any{"action": "preplan_context", "topic": "auth flow"}

	// First call: the file is absent, so the call creates it.
	env := callTool(t, c, "plan_support", args)
	if !env.OK {
		t.Fatalf("preplan_context: not ok: code=%s body=%s", env.Code, env.Body)
	}
	// The server dates the name with its own clock: read the name from the folder.
	names := preplanDirEntries(t, preplanDir)
	if len(names) != 1 || !regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}-auth-flow\.md$`).MatchString(names[0]) {
		t.Fatalf("preplan dir entries = %v, want one YYYY-MM-DD-auth-flow.md", names)
	}
	name := names[0]
	file := filepath.Join(preplanDir, name)
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
		"Created topic file " + name + ".",
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
		"Topic file " + name + " exists.",
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
	if names := preplanDirEntries(t, preplanDir); len(names) != 1 || names[0] != name {
		t.Errorf("preplan dir entries = %v, want only [%s]", names, name)
	}
}

// preplanCheckLineRe matches the guardrail check line that preplan_check writes.
// The server stamps the time with its own clock, so the test matches the shape.
var preplanCheckLineRe = regexp.MustCompile(`(?m)^\*\*Guardrail check:\*\* \d+ guardrails, (\d+ proposals: \d+ pass, \d+ kept, \d+ dropped)\. Last check: \d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z\.$`)

// TestMCPStdio_PreplanCheck calls plan_support with action preplan_check
// through the real subprocess. It asserts that the first call writes exactly
// one check line after the Started line, that a second call replaces that line
// (still one line, new proposal count), and that a topic with no file returns a
// domain error and creates no file.
func TestMCPStdio_PreplanCheck(t *testing.T) {
	dir := realPath(t, t.TempDir())
	runGit(t, dir, "init", "-q")
	preplanDir := filepath.Join(dir, ".sdlc-v2", "preplan")

	c := connectStdioClient(t, dir)

	// preplan_context creates the topic file that preplan_check then edits.
	env := callTool(t, c, "plan_support", map[string]any{"action": "preplan_context", "topic": "auth flow"})
	if !env.OK {
		t.Fatalf("preplan_context: not ok: code=%s body=%s", env.Code, env.Body)
	}
	names := preplanDirEntries(t, preplanDir)
	if len(names) != 1 {
		t.Fatalf("preplan dir entries = %v, want one topic file", names)
	}
	file := filepath.Join(preplanDir, names[0])

	// checkLines reads the topic file and returns the count text of every check
	// line in it (for example "1 proposals: 1 pass, 0 kept, 0 dropped"), plus the
	// file text for failure messages.
	checkLines := func(step string) ([]string, string) {
		t.Helper()
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("%s: read topic file: %v", step, err)
		}
		var counts []string
		for _, m := range preplanCheckLineRe.FindAllStringSubmatch(string(data), -1) {
			counts = append(counts, m[1])
		}
		return counts, string(data)
	}

	// First call: one check line appears right after the Started line.
	env = callTool(t, c, "plan_support", map[string]any{
		"action": "preplan_check", "topic": "auth flow",
		"passCount": 1, "keptCount": 0, "droppedCount": 0,
	})
	if !env.OK {
		t.Fatalf("preplan_check: not ok: code=%s body=%s", env.Code, env.Body)
	}
	counts, text := checkLines("first call")
	if want := "1 proposals: 1 pass, 0 kept, 0 dropped"; len(counts) != 1 || counts[0] != want {
		t.Fatalf("first call: check line counts = %q, want [%q]; file:\n%s", counts, want, text)
	}
	lines := strings.Split(text, "\n")
	started := -1
	for i, l := range lines {
		if strings.HasPrefix(l, "**Started:**") {
			started = i
			break
		}
	}
	// The skeleton separates paragraphs with a blank line, so the check line is
	// the next non-blank line after the Started line.
	next := started + 1
	for next < len(lines) && lines[next] == "" {
		next++
	}
	if started < 0 || next >= len(lines) || !preplanCheckLineRe.MatchString(lines[next]) {
		t.Errorf("first call: want the check line after the Started line, file:\n%s", text)
	}
	for _, want := range []string{"- preplanFile: " + file, "- guardrailCheckLine: **Guardrail check:** "} {
		if !strings.Contains(env.Body, want) {
			t.Errorf("first call: want %q in body, got:\n%s", want, env.Body)
		}
	}

	// Second call: the check line is replaced, not added.
	env = callTool(t, c, "plan_support", map[string]any{
		"action": "preplan_check", "topic": "auth flow",
		"passCount": 2, "keptCount": 0, "droppedCount": 0,
	})
	if !env.OK {
		t.Fatalf("preplan_check (second call): not ok: code=%s body=%s", env.Code, env.Body)
	}
	counts, text = checkLines("second call")
	if want := "2 proposals: 2 pass, 0 kept, 0 dropped"; len(counts) != 1 || counts[0] != want {
		t.Fatalf("second call: check line counts = %q, want [%q]; file:\n%s", counts, want, text)
	}

	// Topic with no file: a domain error. The counts are valid, so the call
	// fails at the file lookup and creates no file.
	env = callTool(t, c, "plan_support", map[string]any{
		"action": "preplan_check", "topic": "no such topic",
		"passCount": 1, "keptCount": 0, "droppedCount": 0,
	})
	if env.OK {
		t.Fatalf("preplan_check for a topic with no file: want an error, got ok:\n%s", env.Body)
	}
	if env.Code != "domain" {
		t.Errorf("error code = %q, want domain", env.Code)
	}
	if want := "no topic file for topic"; !strings.Contains(env.Body, want) {
		t.Errorf("want %q in body, got:\n%s", want, env.Body)
	}
	if got := preplanDirEntries(t, preplanDir); len(got) != 1 || got[0] != names[0] {
		t.Errorf("preplan dir entries = %v, want only [%s]", got, names[0])
	}
}
