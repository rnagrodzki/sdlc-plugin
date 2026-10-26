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
	"slices"
	"strings"
	"testing"
)

// preplanWordsSuggestion is the Suggestion text that preplan_context returns
// for a topic with no ASCII letter or digit.
const preplanWordsSuggestion = `Pass a topic name with ASCII letters or digits, for example "auth flow".`

// preplanLegacyTopic is a topic file from before the 7-section skeleton: it has
// the 4 old sections (Goal, Users and effect, Flows, Decisions) and lacks
// Problem and benefit, Risk if wrong and Non-goals.
const preplanLegacyTopic = `# Preplan: legacy topic

**Status:** in progress

**Started:** 2026-01-15T10:00:00Z

## Goal

Ship the old shape.

## Users and effect

Old users.

## Flows

Old flow.

## Decisions

| # | Decision | Reason |
|---|---|---|
| 1 | Keep it | Cheap |
`

// preplanRepairedTopic is preplanLegacyTopic after preplan_context adds the 3
// absent headings. Each heading goes after its nearest earlier section, and no
// other byte changes.
const preplanRepairedTopic = `# Preplan: legacy topic

**Status:** in progress

**Started:** 2026-01-15T10:00:00Z

## Goal

Ship the old shape.

## Problem and benefit

## Users and effect

Old users.

## Risk if wrong

## Non-goals

## Flows

Old flow.

## Decisions

| # | Decision | Reason |
|---|---|---|
| 1 | Keep it | Cheap |
`

// preplanAllSections is the "## " heading of each topic section, in skeleton order.
var preplanAllSections = []string{
	"## Goal", "## Problem and benefit", "## Users and effect", "## Risk if wrong",
	"## Non-goals", "## Flows", "## Decisions",
}

// preplanRenderedList returns the items that the renderer prints for the list
// field key in body. A list with items is "- <key>:" followed by indented
// "  - <item>" lines. An empty list is "- <key>: (none)", and this returns an
// empty non-nil slice. It fails the test when body has neither form.
func preplanRenderedList(t *testing.T, body, key string) []string {
	t.Helper()
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		switch line {
		case "- " + key + ": (none)":
			return []string{}
		case "- " + key + ":":
			items := []string{}
			for _, l := range lines[i+1:] {
				item, ok := strings.CutPrefix(l, "  - ")
				if !ok {
					break
				}
				items = append(items, item)
			}
			return items
		}
	}
	t.Fatalf("want the list field %q in body, got:\n%s", key, body)
	return nil
}

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
// a domain error with a Suggestion and creates no file. The sub-test
// "legacy file gets headings" checks that a 4-section file gets the 3 absent
// headings (addedSections) and that a second call changes nothing.
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

	// A topic file from before the 7-section skeleton gets the 3 absent headings
	// on the first call and nothing on the second. The sub-test uses its own
	// repo and server.
	t.Run("legacy file gets headings", func(t *testing.T) {
		dir := realPath(t, t.TempDir())
		runGit(t, dir, "init", "-q")
		preplanDir := filepath.Join(dir, ".sdlc-v2", "preplan")
		if err := os.MkdirAll(preplanDir, 0o755); err != nil {
			t.Fatalf("make preplan dir: %v", err)
		}
		file := filepath.Join(preplanDir, "2026-01-15-legacy-topic.md")
		if err := os.WriteFile(file, []byte(preplanLegacyTopic), 0o644); err != nil {
			t.Fatalf("write legacy topic file: %v", err)
		}

		c := connectStdioClient(t, dir)
		args := map[string]any{"action": "preplan_context", "topic": "legacy topic"}

		// First call: the file exists, so the call adds the 3 headings in place.
		env := callTool(t, c, "plan_support", args)
		if !env.OK {
			t.Fatalf("preplan_context: not ok: code=%s body=%s", env.Code, env.Body)
		}
		added := []string{"## Problem and benefit", "## Risk if wrong", "## Non-goals"}
		if got := preplanRenderedList(t, env.Body, "addedSections"); !slices.Equal(got, added) {
			t.Errorf("first call: addedSections = %q, want %q; body:\n%s", got, added, env.Body)
		}
		for _, want := range []string{
			"- preplanCreated: false",
			"- preplanFile: " + file,
			"Topic file 2026-01-15-legacy-topic.md exists.",
			"Added 3 section heading(s): ## Problem and benefit, ## Risk if wrong, ## Non-goals.",
		} {
			if !strings.Contains(env.Body, want) {
				t.Errorf("first call: want %q in body, got:\n%s", want, env.Body)
			}
		}
		repaired, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read topic file after the first call: %v", err)
		}
		if string(repaired) != preplanRepairedTopic {
			t.Fatalf("first call: topic file bytes differ\nwant:\n%s\ngot:\n%s", preplanRepairedTopic, repaired)
		}
		if names := preplanDirEntries(t, preplanDir); len(names) != 1 {
			t.Errorf("preplan dir entries = %v, want only the legacy file (no temp file left)", names)
		}

		// Second call: every heading is there, so the call adds none and writes nothing.
		env = callTool(t, c, "plan_support", args)
		if !env.OK {
			t.Fatalf("preplan_context (second call): not ok: code=%s body=%s", env.Code, env.Body)
		}
		if !strings.Contains(env.Body, "- addedSections: (none)") {
			t.Errorf("second call: want %q in body, got:\n%s", "- addedSections: (none)", env.Body)
		}
		after, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read topic file after the second call: %v", err)
		}
		if string(after) != string(repaired) {
			t.Errorf("second call changed the topic file\nwant:\n%s\ngot:\n%s", repaired, after)
		}
	})
}

// preplanCheckLineRe matches the guardrail check line that preplan_check writes.
// The server stamps the time with its own clock, so the test matches the shape.
var preplanCheckLineRe = regexp.MustCompile(`(?m)^\*\*Guardrail check:\*\* \d+ guardrails, (\d+ proposals: \d+ pass, \d+ kept, \d+ dropped)\. Last check: \d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z\.$`)

// TestMCPStdio_PreplanCheck calls plan_support with action preplan_check
// through the real subprocess. It asserts that the first call writes exactly
// one check line after the Started line, that a second call replaces that line
// (still one line, new proposal count), and that a topic with no file returns a
// domain error and creates no file. The sub-test "empty sections listed"
// checks the emptySections list of a new skeleton file as the user fills it.
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

	// emptySections lists every section of a new skeleton file, drops a section
	// once the user fills it, and renders (none) when every section has content.
	t.Run("empty sections listed", func(t *testing.T) {
		dir := realPath(t, t.TempDir())
		runGit(t, dir, "init", "-q")
		c := connectStdioClient(t, dir)

		env := callTool(t, c, "plan_support", map[string]any{"action": "preplan_context", "topic": "auth flow"})
		if !env.OK {
			t.Fatalf("preplan_context: not ok: code=%s body=%s", env.Code, env.Body)
		}
		names := preplanDirEntries(t, filepath.Join(dir, ".sdlc-v2", "preplan"))
		if len(names) != 1 {
			t.Fatalf("preplan dir entries = %v, want one topic file", names)
		}
		file := filepath.Join(dir, ".sdlc-v2", "preplan", names[0])
		check := func(step string) string {
			t.Helper()
			env := callTool(t, c, "plan_support", map[string]any{
				"action": "preplan_check", "topic": "auth flow",
				"passCount": 1, "keptCount": 0, "droppedCount": 0,
			})
			if !env.OK {
				t.Fatalf("%s: preplan_check: not ok: code=%s body=%s", step, env.Code, env.Body)
			}
			return env.Body
		}

		// A new skeleton file: all 7 sections are empty.
		body := check("new skeleton")
		if got := preplanRenderedList(t, body, "emptySections"); !slices.Equal(got, preplanAllSections) {
			t.Errorf("new skeleton: emptySections = %q, want %q; body:\n%s", got, preplanAllSections, body)
		}

		// Fill Goal and add a Decisions row: those two leave the list.
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read topic file: %v", err)
		}
		text := strings.Replace(string(data), "## Goal\n\n", "## Goal\n\nLet users sign in.\n\n", 1)
		text += "| 1 | Use OAuth | Fewer passwords |\n"
		if err := os.WriteFile(file, []byte(text), 0o644); err != nil {
			t.Fatalf("fill topic file: %v", err)
		}
		body = check("goal and decision filled")
		wantEmpty := preplanAllSections[1 : len(preplanAllSections)-1]
		if got := preplanRenderedList(t, body, "emptySections"); !slices.Equal(got, wantEmpty) {
			t.Errorf("goal and decision filled: emptySections = %q, want %q; body:\n%s", got, wantEmpty, body)
		}

		// Fill the other 5 sections: the list is empty and renders (none).
		for _, h := range wantEmpty {
			text = strings.Replace(text, h+"\n\n", h+"\n\nText.\n\n", 1)
		}
		if err := os.WriteFile(file, []byte(text), 0o644); err != nil {
			t.Fatalf("fill topic file: %v", err)
		}
		body = check("every section filled")
		if !strings.Contains(body, "- emptySections: (none)") {
			t.Errorf("every section filled: want %q in body, got:\n%s", "- emptySections: (none)", body)
		}
	})
}
