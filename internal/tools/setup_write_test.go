// Package tools: tests for setup_write_sections's scaffold_ci auto-trigger
// (Task 10 addition).
package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rnagrodzki/sdlc-plugin/internal/fsx"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
)

// callRegisteredSetupWriteSections runs the registered setup_write_sections
// tool over an in-memory MCP session from a fresh non-git directory, so the
// handler falls back to that directory as the project root. It returns the
// result and that directory.
func callRegisteredSetupWriteSections(t *testing.T, sectionsJSON string) (*mcp.CallToolResult, string, string) {
	t.Helper()
	dir := t.TempDir()
	res, text := callRegisteredSetupWriteSectionsIn(t, dir, sectionsJSON)
	return res, text, dir
}

// callRegisteredSetupWriteSectionsIn is callRegisteredSetupWriteSections run
// from a caller-made (and possibly pre-seeded) non-git directory.
func callRegisteredSetupWriteSectionsIn(t *testing.T, dir, sectionsJSON string) (*mcp.CallToolResult, string) {
	t.Helper()
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(dir))
	t.Chdir(dir)

	s := mcpserver.New("test", "0.0.0-test")
	RegisterSetupWriteTools(s)
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

	res, err := c.CallTool(ctx, &mcp.CallToolParams{
		Name:      "setup_write_sections",
		Arguments: map[string]any{"sectionsJson": sectionsJSON},
	})
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

// TestSetupWriteSections_FallbackWarns verifies that a layout the splicer
// cannot edit in place (the section inside an inline table) in a file with
// no comment line is still written correctly by a full rewrite, and that the
// tool warns about it.
func TestSetupWriteSections_FallbackWarns(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".sdlc-v2", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("plan = { tasks = { note = \"old\" } }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, text := callRegisteredSetupWriteSectionsIn(t, dir, `{"plan.tasks":{"note":"new"}}`)
	if res.IsError {
		t.Fatalf("expected success, got tool error:\n%s", text)
	}
	if !strings.Contains(text, "Section plan.tasks: could not edit .sdlc-v2/config.toml in place; the file had no comments, so it was rewritten and template tips were added") {
		t.Errorf("missing full-rewrite warning:\n%s", text)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "note = 'new'") {
		t.Errorf("plan.tasks.note not written:\n%s", got)
	}
}

// TestSetupWriteSections_FallbackKeepsIntegers verifies that the full-rewrite
// fallback writes whole numbers in the other sections as integers, not as
// floats such as "60.0".
func TestSetupWriteSections_FallbackKeepsIntegers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".sdlc-v2", "local.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	content := "workspace = { tasks = { note = \"old\" } }\n\n[ship]\nexecuteWaveInterval = 60\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	res, text := callRegisteredSetupWriteSectionsIn(t, dir, `{"workspace.tasks":{"note":"new"}}`)
	if res.IsError {
		t.Fatalf("expected success, got tool error:\n%s", text)
	}
	if !strings.Contains(text, "Section workspace.tasks: could not edit .sdlc-v2/local.toml in place; the file had no comments, so it was rewritten and template tips were added") {
		t.Errorf("missing full-rewrite warning:\n%s", text)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "executeWaveInterval = 60\n") || strings.Contains(string(got), "60.0") {
		t.Errorf("fallback rewrite turned the [ship] integer into a float:\n%s", got)
	}
}

// TestSetupWriteSections_KeepsTemplateComments verifies that writing one
// section into the commented config.toml/local.toml templates replaces only
// that section's text: every comment and every other section stays
// byte-for-byte, and the file decodes to the written values.
func TestSetupWriteSections_KeepsTemplateComments(t *testing.T) {
	cases := []struct {
		name     string
		file     string
		template string
		json     string
		oldBlock string // exact template text of the replaced section
		newBlock string // exact spliced text
	}{
		{
			name:     "config.toml commit",
			file:     "config.toml",
			template: configTemplate,
			json:     `{"commit":{"allowedTypes":["feat","fix"],"allowedScopes":["api"]}}`,
			oldBlock: "[commit]\n" +
				"# Allowed commit types (conventional-commit prefix before the colon).\n" +
				"allowedTypes = [\"feat\", \"fix\", \"chore\", \"docs\", \"refactor\", \"test\", \"ci\", \"perf\"]\n" +
				"# Allowed scopes (empty = any scope accepted).\n" +
				"allowedScopes = []\n",
			newBlock: "[commit]\n" +
				"# Allowed commit types (conventional-commit prefix before the colon).\n" +
				"allowedTypes = ['feat', 'fix']\n" +
				"# Allowed scopes (empty = any scope accepted).\n" +
				"allowedScopes = ['api']\n",
		},
		{
			name:     "config.toml dotted plan.guardrails",
			file:     "config.toml",
			template: configTemplate,
			json:     `{"plan.guardrails":{"only-one":{"severity":"warning","description":"d"}}}`,
			oldBlock: "[plan.guardrails.test-coverage-required]\n" +
				"severity = \"error\"\n" +
				"description = \"\"\"\n" +
				"Every task that creates or modifies source code \\\n" +
				"must include corresponding test cases.\"\"\"\n" +
				"\n" +
				"[plan.guardrails.no-ci-bypass]\n" +
				"severity = \"error\"\n" +
				"description = \"Plans must not include steps that skip or disable CI checks.\"\n",
			newBlock: "[plan.guardrails.only-one]\ndescription = 'd'\nseverity = 'warning'\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !strings.Contains(tc.template, tc.oldBlock) {
				t.Fatalf("template no longer contains the expected block:\n%s", tc.oldBlock)
			}
			dir := t.TempDir()
			path := filepath.Join(dir, ".sdlc-v2", tc.file)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(tc.template), 0o644); err != nil {
				t.Fatal(err)
			}

			res, text := callRegisteredSetupWriteSectionsIn(t, dir, tc.json)
			if res.IsError {
				t.Fatalf("expected success, got tool error:\n%s", text)
			}
			if strings.Contains(text, "could not edit") {
				t.Errorf("tool fell back to a full rewrite:\n%s", text)
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			want := strings.Replace(tc.template, tc.oldBlock, tc.newBlock, 1)
			if string(got) != want {
				t.Errorf("file is not the template with only the section replaced.\n--- got ---\n%s\n--- want ---\n%s", got, want)
			}
		})
	}
}

// TestSetupWriteSections_UnknownTopLevelKeyRejected verifies that a section
// key whose first segment is neither a project nor a local section (e.g. a
// typo) is rejected with a DomainError naming the key and the allowed keys,
// and that nothing is written — not even the valid keys in the same call.
func TestSetupWriteSections_UnknownTopLevelKeyRejected(t *testing.T) {
	cases := map[string]struct {
		sectionsJSON string
		unknown      string
	}{
		"typo of a local key":        {`{"shp":{"draft":true}}`, "shp"},
		"dotted unknown key":         {`{"foo.bar":{"x":1}}`, "foo.bar"},
		"unknown next to valid keys": {`{"commit":{"style":"conventional"},"ship":{"draft":true},"reviw":{"x":1}}`, "reviw"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			res, text, dir := callRegisteredSetupWriteSections(t, tc.sectionsJSON)
			if !res.IsError {
				t.Fatalf("expected a tool error, got success:\n%s", text)
			}
			for _, want := range []string{"setup_write_sections: unknown section keys", tc.unknown, "ship", "automation", "commit"} {
				if !strings.Contains(text, want) {
					t.Errorf("error text missing %q:\n%s", want, text)
				}
			}
			for _, f := range []string{"config.toml", "local.toml"} {
				if _, err := os.Stat(filepath.Join(dir, ".sdlc-v2", f)); !os.IsNotExist(err) {
					t.Errorf(".sdlc-v2/%s was written (stat err: %v); want no write", f, err)
				}
			}
		})
	}
}

// TestSetupWriteSections_KnownLocalKeyWritten verifies that a known local
// section key still routes to .sdlc-v2/local.toml.
func TestSetupWriteSections_KnownLocalKeyWritten(t *testing.T) {
	res, text, dir := callRegisteredSetupWriteSections(t, `{"planStyle":{"style":"compact"}}`)
	if res.IsError {
		t.Fatalf("expected success, got tool error:\n%s", text)
	}
	data, err := os.ReadFile(filepath.Join(dir, ".sdlc-v2", "local.toml"))
	if err != nil {
		t.Fatalf("read local.toml: %v", err)
	}
	if !strings.Contains(string(data), "planStyle") {
		t.Errorf("local.toml has no planStyle table:\n%s", data)
	}
	if _, err := os.Stat(filepath.Join(dir, ".sdlc-v2", "config.toml")); !os.IsNotExist(err) {
		t.Errorf("config.toml was written for a local key (stat err: %v)", err)
	}
}

// TestSetupWriteSections_VersionTriggersScaffold verifies that writing a
// "version" section auto-triggers scaffold_ci and reports the resulting
// file actions on the output.
func TestSetupWriteSections_VersionTriggersScaffold(t *testing.T) {
	root := t.TempDir()

	out, err := setupWriteSections(root, root, SetupWriteSectionsIn{
		SectionsJSON: `{"version":{"tag.enabled":true,"tag.prefix":"v"}}`,
	})
	if err != nil {
		t.Fatalf("setupWriteSections: %v", err)
	}
	if !out.OK {
		t.Fatalf("expected OK=true, got errors: %v", out.Errors)
	}
	if len(out.Written) != 1 || out.Written[0] != "version" {
		t.Fatalf("expected written=[version], got %v", out.Written)
	}

	if len(out.Scaffold) != 8 {
		t.Fatalf("expected 8 scaffold file reports, got %d", len(out.Scaffold))
	}
	for _, f := range out.Scaffold {
		if f.Action != "created" {
			t.Errorf("file %s: expected action 'created', got %q", f.Path, f.Action)
		}
	}

	destPath := filepath.Join(root, ".github", "workflows", "release-on-main.yml")
	if !scaffoldFileExists(destPath) {
		t.Errorf("release-on-main.yml not written to disk at %s", destPath)
	}
}

// TestSetupWriteSections_NonVersionSkipsScaffold verifies that writing a
// section batch without "version" does not trigger scaffold_ci.
func TestSetupWriteSections_NonVersionSkipsScaffold(t *testing.T) {
	root := t.TempDir()

	out, err := setupWriteSections(root, root, SetupWriteSectionsIn{
		SectionsJSON: `{"commit":{"style":"conventional"}}`,
	})
	if err != nil {
		t.Fatalf("setupWriteSections: %v", err)
	}
	if !out.OK {
		t.Fatalf("expected OK=true, got errors: %v", out.Errors)
	}
	if out.Scaffold != nil {
		t.Errorf("expected no scaffold reports, got %v", out.Scaffold)
	}

	destPath := filepath.Join(root, ".github", "workflows", "release-on-main.yml")
	if scaffoldFileExists(destPath) {
		t.Errorf("release-on-main.yml unexpectedly written to disk at %s", destPath)
	}
}

// TestSetupWriteSections_VersionScaffoldIdempotent verifies a second call
// with "version" re-triggers scaffold_ci non-destructively (all skipped,
// no error, config write still succeeds).
func TestSetupWriteSections_VersionScaffoldIdempotent(t *testing.T) {
	root := t.TempDir()

	if _, err := setupWriteSections(root, root, SetupWriteSectionsIn{
		SectionsJSON: `{"version":{"tag.enabled":true,"tag.prefix":"v"}}`,
	}); err != nil {
		t.Fatalf("setupWriteSections (first): %v", err)
	}

	out, err := setupWriteSections(root, root, SetupWriteSectionsIn{
		SectionsJSON: `{"version":{"tag.enabled":true,"tag.prefix":"v"}}`,
	})
	if err != nil {
		t.Fatalf("setupWriteSections (second): %v", err)
	}
	if !out.OK {
		t.Fatalf("expected OK=true, got errors: %v", out.Errors)
	}
	for _, f := range out.Scaffold {
		if f.Action != "skipped" {
			t.Errorf("file %s: expected action 'skipped' on second run, got %q", f.Path, f.Action)
		}
	}
}

// TestSetupWriteSections_LinkedWorktree_SplitsRoots verifies the
// contentRoot/stateRoot split (Task 9, R5): run from a linked worktree,
// config.toml and the scaffolded CI files (triggered by the "version"
// write) land under the linked worktree, while local.toml (a local
// section, "planStyle") lands under the main worktree — and the reported
// root matches the linked worktree.
func TestSetupWriteSections_LinkedWorktree_SplitsRoots(t *testing.T) {
	mainDir, linkedDir := scaffoldWorktreeFixture(t)

	resolvedLinked, err := filepath.EvalSymlinks(linkedDir)
	if err != nil {
		t.Fatal(err)
	}

	res, text := callRegisteredSetupWriteSectionsIn(t, linkedDir,
		`{"version":{"tag.enabled":true,"tag.prefix":"v"},"planStyle":{"style":"compact"}}`)
	if res.IsError {
		t.Fatalf("setup_write_sections from a linked worktree returned an error:\n%s", text)
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

	if !scaffoldFileExists(filepath.Join(linkedDir, ".github", "workflows", "release-on-main.yml")) {
		t.Error("release-on-main.yml not scaffolded under the linked worktree")
	}
	if scaffoldFileExists(filepath.Join(mainDir, ".github", "workflows", "release-on-main.yml")) {
		t.Error("release-on-main.yml must not be scaffolded under the main worktree")
	}

	if !scaffoldFileExists(filepath.Join(mainDir, ".sdlc-v2", "local.toml")) {
		t.Error("local.toml not written under the main worktree")
	}
	if scaffoldFileExists(filepath.Join(linkedDir, ".sdlc-v2", "local.toml")) {
		t.Error("local.toml must not be written under the linked worktree")
	}
}

// writeSDLCFile writes content to .sdlc-v2/<name> under dir and returns the
// file path.
func writeSDLCFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, ".sdlc-v2", name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestSetupWriteSections_WholeNumbersWrittenAsIntegers verifies that whole
// JSON numbers are written as TOML integers ("60", not "60.0") at every
// depth, while a number with a fraction stays a float.
func TestSetupWriteSections_WholeNumbersWrittenAsIntegers(t *testing.T) {
	dir := t.TempDir()
	path := writeSDLCFile(t, dir, "local.toml", "# local settings\n[review]\nscope = \"all\"\n")

	// testList is not a real review key; local.toml sections are not
	// key-checked, and it exercises numbers inside an array.
	res, text := callRegisteredSetupWriteSectionsIn(t, dir,
		`{"automation":{"reviewFixIterations":3,"drift":{"maxErrorRate":0.5,"minErrorFloor":2}},`+
			`"review":{"scope":"all","testList":[1,2.5]}}`)
	if res.IsError {
		t.Fatalf("expected success, got tool error:\n%s", text)
	}
	if strings.Contains(text, "could not edit") {
		t.Fatalf("tool fell back to a full rewrite:\n%s", text)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"reviewFixIterations = 3\n",
		"minErrorFloor = 2\n",
		"maxErrorRate = 0.5\n",
		"testList = [1, 2.5]\n",
		"# local settings\n",
	} {
		if !strings.Contains(string(got), want) {
			t.Errorf("local.toml missing %q:\n%s", want, got)
		}
	}
	for _, bad := range []string{"3.0", "2.0", "1.0"} {
		if strings.Contains(string(got), bad) {
			t.Errorf("local.toml has whole number written as float %q:\n%s", bad, got)
		}
	}
}

// TestSetupWriteSections_KeepsCRLFLineEndings verifies that a file with
// CRLF line endings keeps them after a replaced section and an appended
// section are written, so the file never ends up with mixed line endings.
func TestSetupWriteSections_KeepsCRLFLineEndings(t *testing.T) {
	dir := t.TempDir()
	orig := "# project settings\r\n[commit]\r\n# old comment\r\nallowedTypes = [\"feat\"]\r\n\r\n# tail comment\r\n"
	path := writeSDLCFile(t, dir, "config.toml", orig)

	res, text := callRegisteredSetupWriteSectionsIn(t, dir,
		`{"commit":{"allowedTypes":["fix"],"subjectPatternError":"line one\nline two"},"jira":{"defaultProject":"PROJ"}}`)
	if res.IsError {
		t.Fatalf("expected success, got tool error:\n%s", text)
	}
	if strings.Contains(text, "could not edit") {
		t.Fatalf("tool fell back to a full rewrite:\n%s", text)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "# project settings\r\n" +
		"[commit]\r\n# old comment\r\nallowedTypes = ['fix']\r\nsubjectPatternError = \"line one\\nline two\"\r\n" +
		"\r\n# tail comment\r\n" +
		"\r\n[jira]\r\n# Default Jira project key (2–10 uppercase letters, e.g. \"PROJ\").\r\ndefaultProject = 'PROJ'\r\n"
	if string(got) != want {
		t.Errorf("unexpected file.\n--- got ---\n%q\n--- want ---\n%q", got, want)
	}
	if n := strings.Count(string(got), "\n"); n != strings.Count(string(got), "\r\n") {
		t.Errorf("file has LF line endings without CR:\n%q", got)
	}
}

// TestSetupWriteSections_NullClearsLeaf verifies the spec'd null handling:
// a null section value writes an empty table at that key, which clears its
// fields, while sibling tables and the comments around them stay.
func TestSetupWriteSections_NullClearsLeaf(t *testing.T) {
	dir := t.TempDir()
	path := writeSDLCFile(t, dir, "config.toml",
		"# top\n[plan.guardrails.a]\nseverity = \"error\"\n\n"+
			"# tasks doc\n[plan.tasks]\ncontractShape = \"strict\"\nrequiredFields = [\"x\"]\n\n"+
			"[jira]\ndefaultProject = \"P\"\n")

	res, text := callRegisteredSetupWriteSectionsIn(t, dir, `{"plan.tasks":null}`)
	if res.IsError {
		t.Fatalf("expected success, got tool error:\n%s", text)
	}
	if !strings.Contains(text, "- ok: true") || !strings.Contains(text, "  - plan.tasks") {
		t.Errorf("result does not report plan.tasks as written:\n%s", text)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "# top\n[plan.guardrails.a]\nseverity = \"error\"\n\n" +
		"# tasks doc\n[plan.tasks]\n\n" +
		"[jira]\ndefaultProject = \"P\"\n"
	if string(got) != want {
		t.Errorf("plan.tasks not cleared to an empty table.\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// shipSectionLines returns the lines of localTemplate's top-level "ship"
// section (the lines strictly between the "[ship]" header and the next
// top-level header), split on "\n".
func shipSectionLines(t *testing.T) []string {
	t.Helper()
	lines := strings.Split(localTemplate, "\n")
	start := -1
	for i, l := range lines {
		if l == "[ship]" {
			start = i + 1
			break
		}
	}
	if start < 0 {
		t.Fatal("localTemplate has no [ship] header")
	}
	end := len(lines)
	for i := start; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], "[") {
			end = i
			break
		}
	}
	return lines[start:end]
}

// shipTips independently recomputes, straight from localTemplate's text (not
// a hardcoded literal), the comment block directly above each "key = value"
// line of the "ship" section, keyed by that exact key/value line. A key with
// no comment directly above it (no blank line between) is left out. This
// mirrors config.RestoreTips' own notion of a tip, so a future wording change
// to a ship tip (e.g. Task 6's rebase tip) needs no change here.
func shipTips(t *testing.T) map[string]string {
	t.Helper()
	lines := shipSectionLines(t)
	isComment := func(l string) bool { return strings.HasPrefix(strings.TrimSpace(l), "#") }
	tips := make(map[string]string)
	for i, l := range lines {
		if l == "" || isComment(l) || !strings.Contains(l, "=") {
			continue
		}
		if i == 0 || !isComment(lines[i-1]) {
			continue
		}
		start := i - 1
		for start > 0 && isComment(lines[start-1]) {
			start--
		}
		tips[l] = strings.Join(lines[start:i], "\n") + "\n"
	}
	if len(tips) == 0 {
		t.Fatal("test fixture assumption broke: localTemplate's [ship] section has no key with a tip above it")
	}
	return tips
}

// shipSectionValues decodes the "ship" table out of localTemplate, as the
// field-value object setup_write_sections expects for sectionsJson. Values
// come straight from the template so the test exercises real field values,
// not an invented fixture.
func shipSectionValues(t *testing.T) map[string]any {
	t.Helper()
	var full map[string]any
	if err := fsx.DecodeTOML([]byte(localTemplate), &full); err != nil {
		t.Fatalf("decode localTemplate: %v", err)
	}
	ship, ok := full["ship"].(map[string]any)
	if !ok {
		t.Fatal("test fixture assumption broke: localTemplate has no [ship] table")
	}
	return ship
}

// stripTemplateComments removes every comment line from tmpl, leaving every
// other line (including blank ones) untouched, so a write into the result
// must restore any tip from the template rather than finding it already
// there.
func stripTemplateComments(tmpl string) string {
	var out []string
	for _, l := range strings.Split(tmpl, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "#") {
			continue
		}
		out = append(out, l)
	}
	return strings.Join(out, "\n")
}

// TestSetupWriteSections_ShipSectionNoopKeepsEveryTip verifies that writing
// the "ship" section back into the shipped local.toml template with its own
// unchanged values keeps the file byte-for-byte identical — so every ship
// tip (and everything else) survives.
func TestSetupWriteSections_ShipSectionNoopKeepsEveryTip(t *testing.T) {
	tips := shipTips(t) // fixture guard: fails fast if [ship] loses its tips
	if len(tips) == 0 {
		t.Fatal("no ship tips found")
	}

	dir := t.TempDir()
	path := writeSDLCFile(t, dir, "local.toml", localTemplate)

	shipJSON, err := json.Marshal(shipSectionValues(t))
	if err != nil {
		t.Fatalf("marshal ship values: %v", err)
	}
	res, text := callRegisteredSetupWriteSectionsIn(t, dir, `{"ship":`+string(shipJSON)+`}`)
	if res.IsError {
		t.Fatalf("expected success, got tool error:\n%s", text)
	}
	if strings.Contains(text, "could not edit") {
		t.Fatalf("tool fell back to a full rewrite:\n%s", text)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != localTemplate {
		t.Errorf("no-op ship write changed the file.\n--- got ---\n%s\n--- want (unchanged) ---\n%s", got, localTemplate)
	}
}

// TestSetupWriteSections_ShipSectionRestoresStrippedTips verifies that
// writing the "ship" section into a local.toml whose comments were all
// stripped out restores every one of the ship section's tips, derived live
// from localTemplate rather than a hardcoded literal.
func TestSetupWriteSections_ShipSectionRestoresStrippedTips(t *testing.T) {
	tips := shipTips(t)

	dir := t.TempDir()
	stripped := stripTemplateComments(localTemplate)
	path := writeSDLCFile(t, dir, "local.toml", stripped)

	shipJSON, err := json.Marshal(shipSectionValues(t))
	if err != nil {
		t.Fatalf("marshal ship values: %v", err)
	}
	res, text := callRegisteredSetupWriteSectionsIn(t, dir, `{"ship":`+string(shipJSON)+`}`)
	if res.IsError {
		t.Fatalf("expected success, got tool error:\n%s", text)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for keyLine, tip := range tips {
		if !strings.Contains(string(got), tip+keyLine+"\n") {
			t.Errorf("tip not restored directly above %q:\n--- got ---\n%s", keyLine, got)
		}
	}

	// Spec "Lost tips restored": "no section other than ship changes" — the
	// text strictly before "[ship]" and strictly from the next top-level
	// header onward must stay exactly as stripTemplateComments left it (no
	// tip restored into an unwritten, equally-stripped section).
	wantBefore, wantAfter := splitAtShipSection(t, stripped)
	gotBefore, gotAfter := splitAtShipSection(t, string(got))
	if gotBefore != wantBefore {
		t.Errorf("text before [ship] changed:\n--- got ---\n%s\n--- want ---\n%s", gotBefore, wantBefore)
	}
	if gotAfter != wantAfter {
		t.Errorf("text from the section after [ship] onward changed (a tip was restored outside ship):\n--- got ---\n%s\n--- want ---\n%s", gotAfter, wantAfter)
	}
}

// splitAtShipSection splits text at its "[ship]" header: before is every
// line strictly above "[ship]", after is every line from the next
// top-level header (the first following line starting with "[") onward.
// Used to assert that writing the ship section leaves every other section
// of the file untouched, independent of ship's own content.
func splitAtShipSection(t *testing.T, text string) (before, after string) {
	t.Helper()
	lines := strings.Split(text, "\n")
	start := -1
	for i, l := range lines {
		if l == "[ship]" {
			start = i
			break
		}
	}
	if start < 0 {
		t.Fatal("text has no [ship] header")
	}
	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], "[") {
			end = i
			break
		}
	}
	return strings.Join(lines[:start], "\n"), strings.Join(lines[end:], "\n")
}

// TestSetupWriteSections_ErrWouldDropCommentsSetsNext verifies that a
// section write refused because splicing would drop comments
// (config.ErrWouldDropComments) is reported as a per-section error with next
// carrying the hand-edit recovery step, and that the batch still writes
// every other section.
func TestSetupWriteSections_ErrWouldDropCommentsSetsNext(t *testing.T) {
	dir := t.TempDir()
	content := "# kept\nplan = { tasks = { note = \"old\" } }\n"
	path := writeSDLCFile(t, dir, "config.toml", content)

	res, text := callRegisteredSetupWriteSectionsIn(t, dir,
		`{"plan.tasks":{"note":"new"},"commit":{"style":"conventional"}}`)
	if res.IsError {
		t.Fatalf("expected a tool success carrying per-section errors, got tool error:\n%s", text)
	}
	for _, want := range []string{
		"- ok: false",
		"section plan.tasks:",
		"Edit section plan.tasks in .sdlc-v2/config.toml by hand, then run setup again for that section. Other sections were written.",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q:\n%s", want, text)
		}
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "# kept\nplan = { tasks = { note = \"old\" } }\n") {
		t.Errorf("refused section's original text did not survive:\n%s", got)
	}
	if !strings.Contains(string(got), "style = 'conventional'") {
		t.Errorf("the other section (commit) was not written:\n%s", got)
	}
}

// TestSetupWriteSections_ErrWouldDropCommentsFileUnchanged verifies the
// spec's "Layout that cannot be spliced, file with comments" scenario in
// isolation: when the only section selected is the one that gets refused,
// the file is byte-identical, not merely "the refused text survived
// somewhere in a larger file" (TestSetupWriteSections_ErrWouldDropCommentsSetsNext
// above also writes a second, unrelated section that succeeds).
func TestSetupWriteSections_ErrWouldDropCommentsFileUnchanged(t *testing.T) {
	dir := t.TempDir()
	content := "# kept\nplan = { tasks = { note = \"old\" } }\n"
	path := writeSDLCFile(t, dir, "config.toml", content)

	res, text := callRegisteredSetupWriteSectionsIn(t, dir, `{"plan.tasks":{"note":"new"}}`)
	if res.IsError {
		t.Fatalf("expected a tool success carrying a per-section error, got tool error:\n%s", text)
	}
	if !strings.Contains(text, "Edit section plan.tasks in .sdlc-v2/config.toml by hand, then run setup again for that section. Other sections were written.") {
		t.Errorf("missing next recovery text:\n%s", text)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != content {
		t.Errorf("file changed despite the refusal:\n--- got ---\n%s\n--- want (unchanged) ---\n%s", got, content)
	}
}

// TestSetupWriteSections_TwoRefusedSectionsBothInNext verifies that when two
// sections in one call are both refused with config.ErrWouldDropComments,
// each one gets its own errors entry and next names both, in sorted order,
// so neither recovery step is lost.
func TestSetupWriteSections_TwoRefusedSectionsBothInNext(t *testing.T) {
	dir := t.TempDir()
	content := "# kept\nexecute = { waves = { note = \"old\" } }\nplan = { tasks = { note = \"old\" } }\n"
	path := writeSDLCFile(t, dir, "config.toml", content)

	res, text := callRegisteredSetupWriteSectionsIn(t, dir,
		`{"plan.tasks":{"note":"new"},"execute.waves":{"note":"new"}}`)
	if res.IsError {
		t.Fatalf("expected a tool success carrying per-section errors, got tool error:\n%s", text)
	}
	for _, want := range []string{
		"- ok: false",
		"section execute.waves:",
		"section plan.tasks:",
		"Edit section execute.waves in .sdlc-v2/config.toml by hand; Edit section plan.tasks in .sdlc-v2/config.toml by hand, then run setup again for those sections. Other sections were written.",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q:\n%s", want, text)
		}
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != content {
		t.Errorf("file changed despite both refusals:\n--- got ---\n%s\n--- want (unchanged) ---\n%s", got, content)
	}
}
