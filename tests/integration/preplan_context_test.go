//go:build integration

// preplan_context_test.go drives the plan_support actions preplan_context,
// preplan_check and preplan_write through the real sdlc binary over stdio
// (connectStdioClient), the same boundary mcp_stdio_test.go uses. The actions
// write a file under .sdlc-v2/preplan/, so these tests check both the returned
// result and the file on disk.
package integration

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
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

// The closed-set strings of TestMCPStdio_PreplanWrite.
const (
	// preplanWriteTool is the tool that the test calls.
	preplanWriteTool = "plan_support"
	// preplanWriteAction is the plan_support action that the test calls.
	preplanWriteAction = "preplan_write"
	// preplanWriteTopic is the topic name of the test.
	preplanWriteTopic = "auth flow"
	// preplanWriteGoal is the section that the test writes.
	preplanWriteGoal = "Goal"
	// preplanWriteRiskSection is the section that the test seeds and keeps.
	preplanWriteRiskSection = "Risk if wrong"
	// preplanWriteDecisions is the section that gets the bad body.
	preplanWriteDecisions = "Decisions"
	// preplanWriteStatusPart is the entry of the written list for the Status line.
	preplanWriteStatusPart = "Status"
	// preplanWriteStartStatus is the status of a new topic file.
	preplanWriteStartStatus = "in progress"
	// preplanWriteReady is the topicStatus that the test writes.
	preplanWriteReady = "ready for plan"
	// preplanWriteGoalBody is the Goal body that the test writes.
	preplanWriteGoalBody = "Users sign in once."
	// preplanWriteRiskBody is the Risk if wrong body that the test seeds. No
	// write of the test supplies this section, so it must stay.
	preplanWriteRiskBody = "A failed sign-in locks the user out."
	// preplanWriteBadDecisions is a Decisions body with no table header: it
	// breaks rule P4.
	preplanWriteBadDecisions = "Use OAuth because it is simpler."
	// preplanWriteBadRule is the text of the error that the bad body gets.
	preplanWriteBadRule = `Section "` + preplanWriteDecisions + `" breaks rule P4`
	// preplanWriteDomainCode is the error code of a bad input.
	preplanWriteDomainCode = "domain"
	// preplanWriteMode is the mode that the test gives the topic file. The
	// file is created with 0o644, so the rewrite must not reset it.
	preplanWriteMode os.FileMode = 0o640
)

// preplanWriteGoalAndStatus is the argument set of the call that writes the
// Goal body and the ready for plan status.
var preplanWriteGoalAndStatus = map[string]any{
	"section": preplanWriteGoal, "content": preplanWriteGoalBody, "topicStatus": preplanWriteReady,
}

// preplanWriteFixture is the state of TestMCPStdio_PreplanWrite: the stdio
// client, the topic file, and the text of the file after the seed.
type preplanWriteFixture struct {
	c      *mcp.ClientSession
	file   string
	seeded string
}

// preplanReadText returns the text of the file at path, or fails the test.
func preplanReadText(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

// newPreplanWriteFixture starts the server in a new git repo and creates the
// topic file with preplan_context. It then seeds the Risk if wrong section,
// which no preplan_write call of the test supplies, and sets the file mode.
func newPreplanWriteFixture(t *testing.T) preplanWriteFixture {
	t.Helper()
	dir := realPath(t, t.TempDir())
	runGit(t, dir, "init", "-q")
	c := connectStdioClient(t, dir)
	env := callTool(t, c, preplanWriteTool, map[string]any{"action": "preplan_context", "topic": preplanWriteTopic})
	if !env.OK {
		t.Fatalf("preplan_context: not ok: code=%s body=%s", env.Code, env.Body)
	}
	preplanDir := filepath.Join(dir, ".sdlc-v2", "preplan")
	names := preplanDirEntries(t, preplanDir)
	if len(names) != 1 {
		t.Fatalf("preplan dir entries = %v, want one topic file", names)
	}
	file := filepath.Join(preplanDir, names[0])
	skeleton := preplanReadText(t, file)
	riskHead := "## " + preplanWriteRiskSection + "\n\n"
	seeded := strings.Replace(skeleton, riskHead, riskHead+preplanWriteRiskBody+"\n\n", 1)
	if seeded == skeleton {
		t.Fatalf("the skeleton has no %s section:\n%s", preplanWriteRiskSection, skeleton)
	}
	if err := os.WriteFile(file, []byte(seeded), 0o644); err != nil {
		t.Fatalf("seed the topic file: %v", err)
	}
	if err := os.Chmod(file, preplanWriteMode); err != nil {
		t.Fatalf("chmod the topic file: %v", err)
	}
	return preplanWriteFixture{c: c, file: file, seeded: seeded}
}

// write calls preplan_write for the topic with the given arguments added to
// the action and the topic.
func (fx preplanWriteFixture) write(t *testing.T, args map[string]any) renderedResult {
	t.Helper()
	call := map[string]any{"action": preplanWriteAction, "topic": preplanWriteTopic}
	for k, v := range args {
		call[k] = v
	}
	return callTool(t, fx.c, preplanWriteTool, call)
}

// wantGoalAndStatus returns the text of the topic file after the Goal body and
// the ready for plan status are written: the seed with two changes.
func (fx preplanWriteFixture) wantGoalAndStatus(t *testing.T) string {
	t.Helper()
	goalHead := "## " + preplanWriteGoal + "\n\n"
	want := strings.Replace(fx.seeded, goalHead, goalHead+preplanWriteGoalBody+"\n\n", 1)
	want = strings.Replace(want, "**Status:** "+preplanWriteStartStatus, "**Status:** "+preplanWriteReady, 1)
	if strings.Count(want, preplanWriteGoalBody) != 1 || strings.Count(want, preplanWriteReady) != 1 {
		t.Fatalf("the seed has no empty Goal section or no %s status:\n%s", preplanWriteStartStatus, fx.seeded)
	}
	return want
}

// checkFile fails the test unless the topic file holds exactly want and keeps
// preplanWriteMode.
func (fx preplanWriteFixture) checkFile(t *testing.T, step, want string) {
	t.Helper()
	if got := preplanReadText(t, fx.file); got != want {
		t.Errorf("%s: topic file differs\nwant:\n%s\ngot:\n%s", step, want, got)
	}
	info, err := os.Stat(fx.file)
	if err != nil {
		t.Fatalf("%s: stat the topic file: %v", step, err)
	}
	if info.Mode().Perm() != preplanWriteMode {
		t.Errorf("%s: topic file mode = %v, want %v", step, info.Mode().Perm(), preplanWriteMode)
	}
}

// checkWriteResult checks the rendered output of the Goal and status call: the
// file, the written list, and the ready list with its left-out count.
func (fx preplanWriteFixture) checkWriteResult(t *testing.T, env renderedResult) {
	t.Helper()
	if !env.OK {
		t.Fatalf("preplan_write: not ok: code=%s body=%s", env.Code, env.Body)
	}
	if want := "- preplanFile: " + fx.file; !strings.Contains(env.Body, want) {
		t.Errorf("want %q in body, got:\n%s", want, env.Body)
	}
	if got, want := preplanRenderedList(t, env.Body, "written"), []string{preplanWriteGoal, preplanWriteStatusPart}; !slices.Equal(got, want) {
		t.Errorf("written = %q, want %q; body:\n%s", got, want, env.Body)
	}
	if got, want := preplanRenderedList(t, env.Body, "readyTopicFiles"), []string{fx.file}; !slices.Equal(got, want) {
		t.Errorf("readyTopicFiles = %q, want %q; body:\n%s", got, want, env.Body)
	}
	if want := "- readyLeftOut: 0"; !strings.Contains(env.Body, want) {
		t.Errorf("want %q in body, got:\n%s", want, env.Body)
	}
}

// TestMCPStdio_PreplanWrite calls plan_support with action preplan_write
// through the real subprocess. A call with a section and a topicStatus writes
// both: the test asserts the rendered output and the bytes of the file, which
// keeps the Risk if wrong body that the call did not supply and keeps its
// mode. A second equal call leaves the same bytes. A Decisions body with no
// table header returns a domain error that names rule P4 and leaves the file
// as it was.
func TestMCPStdio_PreplanWrite(t *testing.T) {
	fx := newPreplanWriteFixture(t)
	want := fx.wantGoalAndStatus(t)

	env := fx.write(t, preplanWriteGoalAndStatus)
	fx.checkWriteResult(t, env)
	fx.checkFile(t, "first call", want)
	if !strings.Contains(preplanReadText(t, fx.file), preplanWriteRiskBody) {
		t.Errorf("the write dropped the seeded %s body %q", preplanWriteRiskSection, preplanWriteRiskBody)
	}

	// A second equal call gives the same file.
	env = fx.write(t, preplanWriteGoalAndStatus)
	fx.checkWriteResult(t, env)
	fx.checkFile(t, "second equal call", want)

	// A bad Decisions body is a domain error that names the rule, and the
	// file stays as it was.
	env = fx.write(t, map[string]any{"section": preplanWriteDecisions, "content": preplanWriteBadDecisions})
	if env.OK {
		t.Fatalf("preplan_write with a bad Decisions body: want an error, got ok:\n%s", env.Body)
	}
	if env.Code != preplanWriteDomainCode {
		t.Errorf("error code = %q, want %q", env.Code, preplanWriteDomainCode)
	}
	if !strings.Contains(env.Body, preplanWriteBadRule) {
		t.Errorf("want %q in body, got:\n%s", preplanWriteBadRule, env.Body)
	}
	fx.checkFile(t, "after the bad body", want)
}
