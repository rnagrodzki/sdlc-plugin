// Package tools: tests for setup_write_sections's scaffold_ci auto-trigger
// (Task 10 addition).
package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rnagrodzki/sdlc-plugin/internal/config"
	"github.com/rnagrodzki/sdlc-plugin/internal/fsx"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/shipmeta"
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
	return callRegisteredSetupWriteSectionsInArgs(t, dir, map[string]any{"sectionsJson": sectionsJSON})
}

// callRegisteredSetupWriteSectionsInArgs is callRegisteredSetupWriteSectionsIn
// generalized over the full call arguments, so a test can also pass "target"
// (or any other future field) alongside "sectionsJson".
func callRegisteredSetupWriteSectionsInArgs(t *testing.T, dir string, args map[string]any) (*mcp.CallToolResult, string) {
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
		Arguments: args,
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
	// subjectPatternError has no comment of its own in orig, so it picks up
	// the shipped config.toml template's commented-example tip, same as any
	// other key restored from the whole file (internal/config/tips.go).
	want := "# project settings\r\n" +
		"[commit]\r\n# old comment\r\nallowedTypes = ['fix']\r\n" +
		"# Human-readable error message when subject pattern fails.\r\n" +
		"subjectPatternError = \"line one\\nline two\"\r\n" +
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
	// jira keeps its own comment directly above defaultProject so the
	// whole-file tip restore (tips.go) never touches it: this test pins
	// plan.tasks clearing, not tip restore in an unrelated section.
	path := writeSDLCFile(t, dir, "config.toml",
		"# top\n[plan.guardrails.a]\nseverity = \"error\"\n\n"+
			"# tasks doc\n[plan.tasks]\ncontractShape = \"strict\"\nrequiredFields = [\"x\"]\n\n"+
			"[jira]\n# own comment\ndefaultProject = \"P\"\n")

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
		"[jira]\n# own comment\ndefaultProject = \"P\"\n"
	if string(got) != want {
		t.Errorf("plan.tasks not cleared to an empty table.\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// shipSectionLinesOf returns the lines of tmpl's top-level "ship" section
// (the lines strictly between the "[ship]" header and the next top-level
// header), split on "\n". Generalized over shipSectionLines so a test can
// run it against a modified copy of localTemplate.
func shipSectionLinesOf(t *testing.T, tmpl string) []string {
	t.Helper()
	lines := strings.Split(tmpl, "\n")
	start := -1
	for i, l := range lines {
		if l == "[ship]" {
			start = i + 1
			break
		}
	}
	if start < 0 {
		t.Fatal("template has no [ship] header")
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

// shipSectionLines returns shipSectionLinesOf(t, localTemplate): the shipped
// template's own "ship" section lines.
func shipSectionLines(t *testing.T) []string {
	t.Helper()
	return shipSectionLinesOf(t, localTemplate)
}

// shipSectionUncommentedTemplate returns localTemplate with every commented
// "# key = value" example line inside the top-level "ship" section turned
// live (its "#" prefix removed) — every other line, including each example's
// own tip comment, is untouched. Task 11 ships every [ship] key as a
// commented example (ShipBuiltInDefaults applies at runtime instead), so
// [ship] itself has no live key for TestSetupWriteSections_ShipSection* to
// exercise the splice/tip-restore write path with; this reconstructs a
// fixture that does, straight from the shipped text rather than a
// hand-written literal, so a future ship tip or key changes with it.
func shipSectionUncommentedTemplate(t *testing.T) string {
	t.Helper()
	lines := strings.Split(localTemplate, "\n")
	start, end := -1, -1
	for i, l := range lines {
		if l == "[ship]" {
			start = i + 1
			break
		}
	}
	if start < 0 {
		t.Fatal("localTemplate has no [ship] header")
	}
	end = len(lines)
	for i := start; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], "[") {
			end = i
			break
		}
	}
	uncommented := 0
	for i := start; i < end; i++ {
		trimmed := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(trimmed, "#") || !templateKVRe.MatchString(lines[i]) {
			continue
		}
		lines[i] = strings.TrimSpace(strings.TrimPrefix(trimmed, "#"))
		uncommented++
	}
	if uncommented == 0 {
		t.Fatal("test fixture assumption broke: localTemplate's [ship] section has no commented key=value example to uncomment")
	}
	return strings.Join(lines, "\n")
}

// shipTipsOf independently recomputes, straight from tmpl's text (not a
// hardcoded literal), the comment block directly above each "key = value"
// line of its "ship" section, keyed by that exact key/value line. A key with
// no comment directly above it (no blank line between) is left out. This
// mirrors config.RestoreTips' own notion of a tip, so a future wording change
// to a ship tip (e.g. Task 6's rebase tip) needs no change here. Generalized
// over shipTips so a test can run it against a modified copy of
// localTemplate (see shipSectionUncommentedTemplate).
func shipTipsOf(t *testing.T, tmpl string) map[string]string {
	t.Helper()
	lines := shipSectionLinesOf(t, tmpl)
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
		t.Fatal("test fixture assumption broke: the template's [ship] section has no key with a tip above it")
	}
	return tips
}

// shipTips returns shipTipsOf(t, localTemplate): the shipped template's own
// "ship" section tips.
func shipTips(t *testing.T) map[string]string {
	t.Helper()
	return shipTipsOf(t, localTemplate)
}

// shipSectionValuesOf decodes the "ship" table out of tmpl, as the
// field-value object setup_write_sections expects for sectionsJson.
// Generalized over shipSectionValues so a test can run it against a
// modified copy of localTemplate (see shipSectionUncommentedTemplate).
func shipSectionValuesOf(t *testing.T, tmpl string) map[string]any {
	t.Helper()
	var full map[string]any
	if err := fsx.DecodeTOML([]byte(tmpl), &full); err != nil {
		t.Fatalf("decode template: %v", err)
	}
	ship, ok := full["ship"].(map[string]any)
	if !ok {
		t.Fatal("test fixture assumption broke: template has no [ship] table")
	}
	return ship
}

// shipSectionValues decodes the "ship" table out of localTemplate, as the
// field-value object setup_write_sections expects for sectionsJson. Values
// come straight from the template so the test exercises real field values,
// not an invented fixture. Since Task 11, the shipped [ship] table has no
// live key, so this decodes to an empty map — callers that need a populated
// ship fixture use shipSectionValuesOf with shipSectionUncommentedTemplate
// instead.
func shipSectionValues(t *testing.T) map[string]any {
	t.Helper()
	return shipSectionValuesOf(t, localTemplate)
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
	// Task 11: the shipped [ship] section has no live key of its own
	// (ShipBuiltInDefaults applies at runtime instead), so this exercises
	// the write path against a reconstructed fixture that does.
	tmpl := shipSectionUncommentedTemplate(t)
	tips := shipTipsOf(t, tmpl) // fixture guard: fails fast if [ship] loses its tips
	if len(tips) == 0 {
		t.Fatal("no ship tips found")
	}

	dir := t.TempDir()
	path := writeSDLCFile(t, dir, "local.toml", tmpl)

	shipJSON, err := json.Marshal(shipSectionValuesOf(t, tmpl))
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
	if string(got) != tmpl {
		t.Errorf("no-op ship write changed the file.\n--- got ---\n%s\n--- want (unchanged) ---\n%s", got, tmpl)
	}
}

// TestSetupWriteSections_ShipSectionRestoresStrippedTips verifies that
// writing the "ship" section into a local.toml whose comments were all
// stripped out restores every one of the ship section's tips, derived live
// from localTemplate rather than a hardcoded literal.
func TestSetupWriteSections_ShipSectionRestoresStrippedTips(t *testing.T) {
	tmpl := shipSectionUncommentedTemplate(t) // see TestSetupWriteSections_ShipSectionNoopKeepsEveryTip
	tips := shipTipsOf(t, tmpl)

	dir := t.TempDir()
	stripped := stripTemplateComments(tmpl)
	path := writeSDLCFile(t, dir, "local.toml", stripped)

	shipJSON, err := json.Marshal(shipSectionValuesOf(t, tmpl))
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

// TestSetupWriteSections_InvalidTargetRejected verifies that a target value
// outside the "project"/"user" enum is rejected with the exact Suggestion
// from the task contract's routing table, and that nothing is written.
func TestSetupWriteSections_InvalidTargetRejected(t *testing.T) {
	dir := t.TempDir()
	res, text := callRegisteredSetupWriteSectionsInArgs(t, dir, map[string]any{
		"sectionsJson": `{"style":{"foo":"bar"}}`,
		"target":       "bogus",
	})
	if !res.IsError {
		t.Fatalf("expected a tool error, got success:\n%s", text)
	}
	for _, want := range []string{
		`setup_write_sections: invalid target "bogus"`,
		"Set target to project or user, or leave it empty.",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q:\n%s", want, text)
		}
	}
	for _, f := range []string{"config.toml", "local.toml"} {
		if _, err := os.Stat(filepath.Join(dir, ".sdlc-v2", f)); !os.IsNotExist(err) {
			t.Errorf(".sdlc-v2/%s was written (stat err: %v); want no write", f, err)
		}
	}
}

// TestSetupWriteSections_TargetUserRejectsProjectSection verifies that a
// project section (e.g. "commit") named alongside target "user" is rejected
// with a DomainError, and that nothing is written — not the project
// section, and not the local sections in the same call.
func TestSetupWriteSections_TargetUserRejectsProjectSection(t *testing.T) {
	dir := t.TempDir()
	userPath := filepath.Join(t.TempDir(), "user-local.toml")
	t.Setenv(config.UserConfigPathEnv, userPath)

	res, text := callRegisteredSetupWriteSectionsInArgs(t, dir, map[string]any{
		"sectionsJson": `{"commit":{"style":"conventional"},"style":{"foo":"bar"}}`,
		"target":       "user",
	})
	if !res.IsError {
		t.Fatalf("expected a tool error, got success:\n%s", text)
	}
	for _, want := range []string{
		`cannot use target "user"`,
		"commit",
		"Remove the project section(s) from sectionsJson",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q:\n%s", want, text)
		}
	}
	for _, f := range []string{"config.toml", "local.toml"} {
		if _, err := os.Stat(filepath.Join(dir, ".sdlc-v2", f)); !os.IsNotExist(err) {
			t.Errorf(".sdlc-v2/%s was written (stat err: %v); want no write", f, err)
		}
	}
	if _, err := os.Stat(userPath); !os.IsNotExist(err) {
		t.Errorf("user config file was written (stat err: %v); want no write", err)
	}
}

// TestSetupWriteSections_TargetUserNoHomeDir verifies the routing table's
// last row: target "user", no SDLC_USER_CONFIG override, and no resolvable
// home directory returns the DomainError naming the fix, and writes
// nothing.
func TestSetupWriteSections_TargetUserNoHomeDir(t *testing.T) {
	t.Setenv(config.UserConfigPathEnv, "")
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "") // os.UserHomeDir's Windows equivalent of $HOME

	dir := t.TempDir()
	res, text := callRegisteredSetupWriteSectionsInArgs(t, dir, map[string]any{
		"sectionsJson": `{"style":{"foo":"bar"}}`,
		"target":       "user",
	})
	if !res.IsError {
		t.Fatalf("expected a tool error, got success:\n%s", text)
	}
	for _, want := range []string{
		"setup_write_sections: no user config path available",
		"Set SDLC_USER_CONFIG to a file path, or use target project.",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q:\n%s", want, text)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, ".sdlc-v2", "local.toml")); !os.IsNotExist(err) {
		t.Errorf("local.toml was written (stat err: %v); want no write", err)
	}
}

// TestSetupWriteSections_TargetUserWritesUserConfigPath verifies the
// routing table's "local, user" row: a local section with target "user"
// lands in the resolved user-level file, not the project's own
// .sdlc-v2/local.toml.
func TestSetupWriteSections_TargetUserWritesUserConfigPath(t *testing.T) {
	dir := t.TempDir()
	userPath := filepath.Join(t.TempDir(), "user-local.toml")
	t.Setenv(config.UserConfigPathEnv, userPath)

	res, text := callRegisteredSetupWriteSectionsInArgs(t, dir, map[string]any{
		"sectionsJson": `{"style":{"foo":"bar"}}`,
		"target":       "user",
	})
	if res.IsError {
		t.Fatalf("expected success, got tool error:\n%s", text)
	}
	if !strings.Contains(text, "- ok: true") {
		t.Errorf("expected ok: true:\n%s", text)
	}

	got, err := os.ReadFile(userPath)
	if err != nil {
		t.Fatalf("read user config file: %v", err)
	}
	if !strings.Contains(string(got), "[style]") || !strings.Contains(string(got), "foo = 'bar'") {
		t.Errorf("user config file missing written section:\n%s", got)
	}

	if _, err := os.Stat(filepath.Join(dir, ".sdlc-v2", "local.toml")); !os.IsNotExist(err) {
		t.Errorf("project local.toml was written (stat err: %v); want only the user file", err)
	}
}

// TestSetupWriteSections_TargetProjectExplicitMatchesDefault verifies that
// passing target "project" explicitly behaves exactly like omitting target:
// a local section still lands in the project's own .sdlc-v2/local.toml.
func TestSetupWriteSections_TargetProjectExplicitMatchesDefault(t *testing.T) {
	dir := t.TempDir()
	res, text := callRegisteredSetupWriteSectionsInArgs(t, dir, map[string]any{
		"sectionsJson": `{"planStyle":{"style":"compact"}}`,
		"target":       "project",
	})
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
}

// TestSetupWriteSections_TargetUserTwoCallsBothSectionsPersist verifies the
// acceptance criterion's two-call scenario: writing "style" to the user
// file, then "review" to the user file in a second call, leaves both
// sections present afterward — the second write must not clobber the
// first.
func TestSetupWriteSections_TargetUserTwoCallsBothSectionsPersist(t *testing.T) {
	dir := t.TempDir()
	userPath := filepath.Join(t.TempDir(), "user-local.toml")
	t.Setenv(config.UserConfigPathEnv, userPath)

	if res, text := callRegisteredSetupWriteSectionsInArgs(t, dir, map[string]any{
		"sectionsJson": `{"style":{"foo":"bar"}}`,
		"target":       "user",
	}); res.IsError {
		t.Fatalf("first call: expected success, got tool error:\n%s", text)
	}
	if res, text := callRegisteredSetupWriteSectionsInArgs(t, dir, map[string]any{
		"sectionsJson": `{"review":{"scope":"all"}}`,
		"target":       "user",
	}); res.IsError {
		t.Fatalf("second call: expected success, got tool error:\n%s", text)
	}

	got, err := os.ReadFile(userPath)
	if err != nil {
		t.Fatalf("read user config file: %v", err)
	}
	if !strings.Contains(string(got), "[style]") || !strings.Contains(string(got), "foo = 'bar'") {
		t.Errorf("user config file lost the first call's style section:\n%s", got)
	}
	if !strings.Contains(string(got), "[review]") || !strings.Contains(string(got), "scope = 'all'") {
		t.Errorf("user config file missing the second call's review section:\n%s", got)
	}
}

// TestSetupWriteSections_TargetUserRefusalNamesRealPath verifies that when a
// section write to the user file is refused (config.ErrWouldDropComments),
// the errors and next fields name the real resolved user config path, not
// the project-relative ".sdlc-v2/local.toml" literal used for project-local
// writes.
func TestSetupWriteSections_TargetUserRefusalNamesRealPath(t *testing.T) {
	dir := t.TempDir()
	userDir := t.TempDir()
	userPath := filepath.Join(userDir, "user-local.toml")
	if err := os.WriteFile(userPath, []byte("# kept\nstyle = { foo = { bar = \"old\" } }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(config.UserConfigPathEnv, userPath)

	res, text := callRegisteredSetupWriteSectionsInArgs(t, dir, map[string]any{
		"sectionsJson": `{"style.foo":{"bar":"new"}}`,
		"target":       "user",
	})
	if res.IsError {
		t.Fatalf("expected a tool success carrying a per-section error, got tool error:\n%s", text)
	}
	for _, want := range []string{
		"- ok: false",
		"section style.foo:",
		"Edit section style.foo in " + userPath + " by hand, then run setup again for that section. Other sections were written.",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q:\n%s", want, text)
		}
	}
	got, err := os.ReadFile(userPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "# kept\nstyle = { foo = { bar = \"old\" } }\n" {
		t.Errorf("user config file changed despite the refusal:\n%s", got)
	}
}

// TestSetupWriteSections_TargetUserFallbackWarningNamesRealPath verifies
// that the full-rewrite fallback warning (a file with no comment line) also
// names the real resolved user config path.
func TestSetupWriteSections_TargetUserFallbackWarningNamesRealPath(t *testing.T) {
	dir := t.TempDir()
	userDir := t.TempDir()
	userPath := filepath.Join(userDir, "user-local.toml")
	// A nested inline table (mirrors TestSetupWriteSections_FallbackWarns /
	// _FallbackKeepsIntegers) the splicer cannot edit in place; with no
	// comment line in the file, that forces the full-rewrite fallback this
	// test exercises.
	if err := os.WriteFile(userPath, []byte("style = { tasks = { note = \"old\" } }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(config.UserConfigPathEnv, userPath)

	res, text := callRegisteredSetupWriteSectionsInArgs(t, dir, map[string]any{
		"sectionsJson": `{"style.tasks":{"note":"new"}}`,
		"target":       "user",
	})
	if res.IsError {
		t.Fatalf("expected success, got tool error:\n%s", text)
	}
	if !strings.Contains(text, "Section style.tasks: could not edit "+userPath+" in place; the file had no comments, so it was rewritten and template tips were added") {
		t.Errorf("missing full-rewrite warning naming the real user path:\n%s", text)
	}
}

// TestSetupWriteSections_TargetUserNoopKeepsEveryTip verifies that writing
// the "ship" section's own unchanged values into the shipped template
// through target "user" keeps the user file byte-for-byte identical, the
// same comment-preserving splice guarantee the project-local.toml path
// already has (TestSetupWriteSections_ShipSectionNoopKeepsEveryTip).
func TestSetupWriteSections_TargetUserNoopKeepsEveryTip(t *testing.T) {
	tmpl := shipSectionUncommentedTemplate(t)
	tips := shipTipsOf(t, tmpl)
	if len(tips) == 0 {
		t.Fatal("no ship tips found")
	}

	dir := t.TempDir()
	userDir := t.TempDir()
	// Named "local.toml", matching UserConfigPath's own default basename
	// (~/.sdlc/local.toml): config.sectionTemplate (and so the tip
	// restore) matches templates by file basename, not by directory, so
	// the fixture must use the real basename to exercise that path.
	userPath := filepath.Join(userDir, "local.toml")
	if err := os.WriteFile(userPath, []byte(tmpl), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(config.UserConfigPathEnv, userPath)

	shipJSON, err := json.Marshal(shipSectionValuesOf(t, tmpl))
	if err != nil {
		t.Fatalf("marshal ship values: %v", err)
	}
	res, text := callRegisteredSetupWriteSectionsInArgs(t, dir, map[string]any{
		"sectionsJson": `{"ship":` + string(shipJSON) + `}`,
		"target":       "user",
	})
	if res.IsError {
		t.Fatalf("expected success, got tool error:\n%s", text)
	}
	if strings.Contains(text, "could not edit") {
		t.Fatalf("tool fell back to a full rewrite:\n%s", text)
	}

	got, err := os.ReadFile(userPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != tmpl {
		t.Errorf("no-op ship write changed the user file.\n--- got ---\n%s\n--- want (unchanged) ---\n%s", got, tmpl)
	}
}

// TestSetupWriteSections_TargetUserRestoresStrippedTips verifies that
// writing the "ship" section through target "user" into a user config file
// whose comments were all stripped out restores every one of the ship
// section's tips — the same tip-restore guarantee the project-local.toml
// path already has (TestSetupWriteSections_ShipSectionRestoresStrippedTips).
func TestSetupWriteSections_TargetUserRestoresStrippedTips(t *testing.T) {
	tmpl := shipSectionUncommentedTemplate(t)
	tips := shipTipsOf(t, tmpl)

	dir := t.TempDir()
	userDir := t.TempDir()
	// Named "local.toml" for the same reason as
	// TestSetupWriteSections_TargetUserNoopKeepsEveryTip above.
	userPath := filepath.Join(userDir, "local.toml")
	stripped := stripTemplateComments(tmpl)
	if err := os.WriteFile(userPath, []byte(stripped), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(config.UserConfigPathEnv, userPath)

	shipJSON, err := json.Marshal(shipSectionValuesOf(t, tmpl))
	if err != nil {
		t.Fatalf("marshal ship values: %v", err)
	}
	res, text := callRegisteredSetupWriteSectionsInArgs(t, dir, map[string]any{
		"sectionsJson": `{"ship":` + string(shipJSON) + `}`,
		"target":       "user",
	})
	if res.IsError {
		t.Fatalf("expected success, got tool error:\n%s", text)
	}

	got, err := os.ReadFile(userPath)
	if err != nil {
		t.Fatal(err)
	}
	for keyLine, tip := range tips {
		if !strings.Contains(string(got), tip+keyLine+"\n") {
			t.Errorf("tip not restored directly above %q:\n--- got ---\n%s", keyLine, got)
		}
	}
}

// readShipTable decodes the local.toml at path and returns its "ship" table.
func readShipTable(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := fsx.DecodeTOML(raw, &decoded); err != nil {
		t.Fatalf("local.toml is not valid TOML: %v\n%s", err, raw)
	}
	ship, ok := decoded["ship"].(map[string]any)
	if !ok {
		t.Fatalf("local.toml has no [ship] table:\n%s", raw)
	}
	return ship
}

// assertFlagTable fails unless ship[key] is a table with one bool entry for
// every canonical step, true for exactly the steps named in on.
func assertFlagTable(t *testing.T, ship map[string]any, key string, on ...string) {
	t.Helper()
	table, ok := ship[key].(map[string]any)
	if !ok {
		t.Fatalf("ship.%s is %T, want a table: %v", key, ship[key], ship[key])
	}
	if len(table) != len(shipmeta.CanonicalSteps) {
		t.Errorf("ship.%s has %d keys, want %d: %v", key, len(table), len(shipmeta.CanonicalSteps), table)
	}
	for _, step := range shipmeta.CanonicalSteps {
		want := slices.Contains(on, step)
		got, isBool := table[step].(bool)
		if !isBool || got != want {
			t.Errorf("ship.%s.%s = %v, want %v", key, step, table[step], want)
		}
	}
}

// TestSetupWriteSections_FlagSetArray verifies that a steps array answer
// replaces an old one-line list with a table of every step, and that the other
// [ship] keys and the comments around them stay.
func TestSetupWriteSections_FlagSetArray(t *testing.T) {
	dir := t.TempDir()
	path := writeSDLCFile(t, dir, "local.toml",
		"[ship]\n# Pipeline steps for /ship.\nsteps = [\"execute\", \"review\", \"harden\"]\n# own note\nbump = \"patch\"\n")

	res, text := callRegisteredSetupWriteSectionsIn(t, dir,
		`{"ship":{"steps":["execute","review","harden"],"bump":"patch"}}`)
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
	if strings.Contains(string(got), "steps = [") {
		t.Errorf("old steps list line is still in the file:\n%s", got)
	}
	if !strings.Contains(string(got), "# own note\nbump = \"patch\"\n") {
		t.Errorf("bump or its comment changed:\n%s", got)
	}
	ship := readShipTable(t, path)
	assertFlagTable(t, ship, "steps", "execute", "review", "harden")
	if ship["bump"] != "patch" {
		t.Errorf("ship.bump = %v, want patch", ship["bump"])
	}
}

// TestSetupWriteSections_FlagSetQuick verifies that a quick answer is stored
// as a [ship.quick] table, in the same way as a steps answer.
func TestSetupWriteSections_FlagSetQuick(t *testing.T) {
	dir := t.TempDir()
	path := writeSDLCFile(t, dir, "local.toml", "[ship]\nquick = [\"execute\"]\nbump = \"minor\"\n")

	res, text := callRegisteredSetupWriteSectionsIn(t, dir,
		`{"ship":{"quick":["execute","pr"],"bump":"minor"}}`)
	if res.IsError {
		t.Fatalf("expected success, got tool error:\n%s", text)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), "quick = [") {
		t.Errorf("old quick list line is still in the file:\n%s", got)
	}
	ship := readShipTable(t, path)
	assertFlagTable(t, ship, "quick", "execute", "pr")
	if ship["bump"] != "minor" {
		t.Errorf("ship.bump = %v, want minor", ship["bump"])
	}
}

// TestSetupWriteSections_FlagSetTablePassThrough verifies that a table answer
// is written with only the keys it names. No other step key is added.
func TestSetupWriteSections_FlagSetTablePassThrough(t *testing.T) {
	dir := t.TempDir()
	path := writeSDLCFile(t, dir, "local.toml", "[ship]\nbump = \"patch\"\n")

	res, text := callRegisteredSetupWriteSectionsIn(t, dir,
		`{"ship":{"steps":{"execute":true,"harden":true},"bump":"patch"}}`)
	if res.IsError {
		t.Fatalf("expected success, got tool error:\n%s", text)
	}

	table, ok := readShipTable(t, path)["steps"].(map[string]any)
	if !ok {
		t.Fatalf("ship.steps is not a table")
	}
	want := map[string]any{"execute": true, "harden": true}
	if len(table) != len(want) || table["execute"] != true || table["harden"] != true {
		t.Errorf("ship.steps = %v, want only %v", table, want)
	}
}

// TestSetupWriteSections_FlagSetMultiLineList verifies that an old multi-line
// steps list is gone after the write.
func TestSetupWriteSections_FlagSetMultiLineList(t *testing.T) {
	dir := t.TempDir()
	path := writeSDLCFile(t, dir, "local.toml",
		"[ship]\nsteps = [\n  \"execute\",\n  \"pr\",\n]\nbump = \"patch\"\n")

	res, text := callRegisteredSetupWriteSectionsIn(t, dir,
		`{"ship":{"steps":["execute","commit"],"bump":"patch"}}`)
	if res.IsError {
		t.Fatalf("expected success, got tool error:\n%s", text)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), "steps = [") || strings.Contains(string(got), "\"pr\",") {
		t.Errorf("old multi-line list is still in the file:\n%s", got)
	}
	assertFlagTable(t, readShipTable(t, path), "steps", "execute", "commit")
}

// TestSetupWriteSections_FlagSetInlineTable verifies that an old inline table
// for steps is gone after the write.
func TestSetupWriteSections_FlagSetInlineTable(t *testing.T) {
	dir := t.TempDir()
	path := writeSDLCFile(t, dir, "local.toml",
		"[ship]\nsteps = { execute = true }\nbump = \"patch\"\n")

	res, text := callRegisteredSetupWriteSectionsIn(t, dir,
		`{"ship":{"steps":["review"],"bump":"patch"}}`)
	if res.IsError {
		t.Fatalf("expected success, got tool error:\n%s", text)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), "steps = {") {
		t.Errorf("old inline table is still in the file:\n%s", got)
	}
	assertFlagTable(t, readShipTable(t, path), "steps", "review")
}

// TestSetupWriteSections_FlagSetHeaderTable verifies that a file that already
// has a [ship.steps] header table ends with exactly one such table, holding
// the new values.
func TestSetupWriteSections_FlagSetHeaderTable(t *testing.T) {
	dir := t.TempDir()
	path := writeSDLCFile(t, dir, "local.toml",
		"[ship]\nbump = \"patch\"\n\n[ship.steps]\nexecute = true\npr = true\n")

	res, text := callRegisteredSetupWriteSectionsIn(t, dir,
		`{"ship":{"steps":["execute","review"],"bump":"patch"}}`)
	if res.IsError {
		t.Fatalf("expected success, got tool error:\n%s", text)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(got), "[ship.steps]"); n != 1 {
		t.Errorf("file has %d [ship.steps] headers, want 1:\n%s", n, got)
	}
	assertFlagTable(t, readShipTable(t, path), "steps", "execute", "review")
}

// TestSetupWriteSections_FlagSetEmpty verifies that an empty array writes
// every step key as false.
func TestSetupWriteSections_FlagSetEmpty(t *testing.T) {
	dir := t.TempDir()
	path := writeSDLCFile(t, dir, "local.toml", "[ship]\nquick = [\"execute\"]\n")

	res, text := callRegisteredSetupWriteSectionsIn(t, dir, `{"ship":{"quick":[]}}`)
	if res.IsError {
		t.Fatalf("expected success, got tool error:\n%s", text)
	}
	assertFlagTable(t, readShipTable(t, path), "quick")
}

// assertFlagSetRejected runs the write for answerJSON (the value of ship.steps)
// next to a second section, and fails unless the tool returns an error that
// holds every string in want, with no section written and the file unchanged.
func assertFlagSetRejected(t *testing.T, answerJSON string, want ...string) {
	t.Helper()
	dir := t.TempDir()
	content := "[ship]\nbump = \"patch\"\n"
	local := writeSDLCFile(t, dir, "local.toml", content)

	res, text := callRegisteredSetupWriteSectionsIn(t, dir,
		`{"review":{"scope":"all"},"ship":{"steps":`+answerJSON+`}}`)
	if !res.IsError {
		t.Fatalf("expected a tool error, got success:\n%s", text)
	}
	for _, w := range want {
		if !strings.Contains(text, w) {
			t.Errorf("missing %q:\n%s", w, text)
		}
	}
	got, err := os.ReadFile(local)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != content {
		t.Errorf("file changed despite the error (the review section was written):\n%s", got)
	}
}

// TestSetupWriteSections_FlagSetUnknownName verifies that an unknown step name
// in an array answer returns a DomainError with the list of valid names.
func TestSetupWriteSections_FlagSetUnknownName(t *testing.T) {
	assertFlagSetRejected(t, `["hardn"]`,
		`ship.steps answer has an unknown step "hardn".`,
		"Use only these names: execute, commit, review, verify-openspec, archive-openspec, harden, pr, verify-pipeline, await-remote-review, learnings-commit.")
}

// TestSetupWriteSections_FlagSetNonBool verifies that a table value that is
// not true or false returns a DomainError.
func TestSetupWriteSections_FlagSetNonBool(t *testing.T) {
	assertFlagSetRejected(t, `{"harden":"yes"}`,
		`ship.steps.harden must be true or false, got "yes".`,
		"Send true or false for each step, or send an array of step names.")
}

// TestSetupWriteSections_FlagSetTableUnknownKey verifies that an unknown key
// in a table answer returns a DomainError that names the key.
func TestSetupWriteSections_FlagSetTableUnknownKey(t *testing.T) {
	assertFlagSetRejected(t, `{"hardn":true}`,
		`ship.steps answer has an unknown step "hardn".`)
}

// TestSetupWriteSections_FlagSetScalar verifies that a scalar answer returns a
// DomainError.
func TestSetupWriteSections_FlagSetScalar(t *testing.T) {
	assertFlagSetRejected(t, `"harden"`,
		"ship.steps answer must be an array of step names or a table of step → true/false, got string.",
		`Send an array of step names, for example ["execute","review"].`)
}

// TestSetupWriteSections_FlagSetRejectedValueTypes verifies that each JSON
// value type that is not a valid flag-set answer is named in the error: a
// bool and a number as the whole answer, a non-string item in an array and a
// number as a table value.
func TestSetupWriteSections_FlagSetRejectedValueTypes(t *testing.T) {
	tests := []struct {
		name   string
		answer string
		want   string
	}{
		{name: "bool answer", answer: `true`, want: "got bool."},
		{name: "number answer", answer: `5`, want: "got number."},
		{name: "number array item", answer: `[1]`, want: "ship.steps answer has an unknown step 1."},
		{name: "number table value", answer: `{"harden":5}`, want: "ship.steps.harden must be true or false, got 5."},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assertFlagSetRejected(t, tc.answer, tc.want)
		})
	}
}

// TestFlagSetTypeName pins the name of each answer type, including the
// fallback to the Go type name for a type that JSON decoding does not produce.
func TestFlagSetTypeName(t *testing.T) {
	tests := []struct {
		value any
		want  string
	}{
		{value: "harden", want: "string"},
		{value: true, want: "bool"},
		{value: float64(5), want: "number"},
		{value: 5, want: "int"},
	}
	for _, tc := range tests {
		if got := flagSetTypeName(tc.value); got != tc.want {
			t.Errorf("flagSetTypeName(%#v) = %q, want %q", tc.value, got, tc.want)
		}
	}
}

// TestSetupWriteSections_FlagSetIdempotent verifies that the same steps answer
// written twice gives the same file bytes.
func TestSetupWriteSections_FlagSetIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := writeSDLCFile(t, dir, "local.toml",
		"[ship]\n# Pipeline steps for /ship.\nsteps = [\"execute\"]\nbump = \"patch\"\n")
	answer := `{"ship":{"steps":["execute","review","harden"],"bump":"patch"}}`

	var afterWrite [2][]byte
	for i := range afterWrite {
		if res, text := callRegisteredSetupWriteSectionsIn(t, dir, answer); res.IsError {
			t.Fatalf("write %d: expected success, got tool error:\n%s", i+1, text)
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		afterWrite[i] = got
	}
	if string(afterWrite[0]) != string(afterWrite[1]) {
		t.Errorf("second write changed the file.\n--- first ---\n%s\n--- second ---\n%s", afterWrite[0], afterWrite[1])
	}
}

// TestNormalizeFlagSetFields pins the cases of normalizeFlagSetFields that the
// tool tests above do not reach: a nil map, a null answer, an id that is not
// a section, and a section with no flag-set field.
func TestNormalizeFlagSetFields(t *testing.T) {
	t.Run("nil values", func(t *testing.T) {
		if err := normalizeFlagSetFields("ship", nil); err != nil {
			t.Errorf("nil values: unexpected error %v", err)
		}
	})
	t.Run("null answer stays", func(t *testing.T) {
		values := map[string]any{"steps": nil}
		if err := normalizeFlagSetFields("ship", values); err != nil {
			t.Fatalf("unexpected error %v", err)
		}
		if v, present := values["steps"]; !present || v != nil {
			t.Errorf("values[steps] = %v (present %v), want a nil value left in place", v, present)
		}
	})
	t.Run("unknown section id", func(t *testing.T) {
		values := map[string]any{"steps": "anything"}
		if err := normalizeFlagSetFields("nosuchsection", values); err != nil {
			t.Fatalf("unexpected error %v", err)
		}
		if values["steps"] != "anything" {
			t.Errorf("values changed for an id that is not a section: %v", values)
		}
	})
	t.Run("section without flag-set field", func(t *testing.T) {
		values := map[string]any{"scope": []any{"not", "touched"}}
		if err := normalizeFlagSetFields("review", values); err != nil {
			t.Fatalf("unexpected error %v", err)
		}
		if _, isList := values["scope"].([]any); !isList {
			t.Errorf("values changed for a section with no flag-set field: %v", values)
		}
	})
	t.Run("array becomes map of bool", func(t *testing.T) {
		values := map[string]any{"steps": []any{"pr"}}
		if err := normalizeFlagSetFields("ship", values); err != nil {
			t.Fatalf("unexpected error %v", err)
		}
		table, ok := values["steps"].(map[string]any)
		if !ok || len(table) != len(shipmeta.CanonicalSteps) || table["pr"] != true || table["execute"] != false {
			t.Errorf("values[steps] = %v, want a bool for every step with only pr true", values["steps"])
		}
	})
}

// guardrailsFile is a config.toml with two plan guardrails and a jira
// section, used by the guardrail delete tests.
const guardrailsFile = "[plan.guardrails.dry]\ndescription = \"No duplicate logic.\"\nseverity = \"warning\"\n\n" +
	"[plan.guardrails.dry-old]\ndescription = \"Do not repeat code.\"\nseverity = \"warning\"\n\n" +
	"[jira]\ndefaultProject = \"P\"\n"

// TestIsGuardrailID verifies which ids isGuardrailID accepts: only
// plan.guardrails.<id> and execute.guardrails.<id> with one id part.
func TestIsGuardrailID(t *testing.T) {
	for id, want := range map[string]bool{
		"plan.guardrails.dry":      true,
		"execute.guardrails.a-b":   true,
		"plan.guardrails":          false,
		"plan.guardrails.":         false,
		"plan.guardrails.a.b":      false,
		"plan.tasks.dry":           false,
		"review.guardrails.dry":    false,
		"execute.guardrails":       false,
		"guardrails.dry":           false,
		"":                         false,
		"plan.Guardrails.dry":      false,
		"harden.guardrails.dry":    false,
		"plan.guardrails.dry.more": false,
	} {
		if got := isGuardrailID(id); got != want {
			t.Errorf("isGuardrailID(%q) = %v, want %v", id, got, want)
		}
	}
}

// TestSetupWriteSections_NullDeletesGuardrail verifies that a JSON null on
// plan.guardrails.<id> or execute.guardrails.<id> removes that table block
// and leaves every other block byte-identical. The file is read back after
// each delete.
func TestSetupWriteSections_NullDeletesGuardrail(t *testing.T) {
	t.Run("plan guardrail", func(t *testing.T) {
		dir := t.TempDir()
		path := writeSDLCFile(t, dir, "config.toml", guardrailsFile)

		res, text := callRegisteredSetupWriteSectionsIn(t, dir, `{"plan.guardrails.dry-old":null}`)
		if res.IsError {
			t.Fatalf("expected success, got tool error:\n%s", text)
		}
		// The id is reported as deleted, with the removed data, and not
		// as written.
		for _, want := range []string{
			"- ok: true",
			"- written: (none)",
			"## deleted[0]\n- id: plan.guardrails.dry-old\n",
			"### deleted[0].table\n- description: Do not repeat code.\n- severity: warning\n",
		} {
			if !strings.Contains(text, want) {
				t.Errorf("result missing %q:\n%s", want, text)
			}
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		want := "[plan.guardrails.dry]\ndescription = \"No duplicate logic.\"\nseverity = \"warning\"\n\n" +
			"[jira]\ndefaultProject = \"P\"\n"
		if string(got) != want {
			t.Errorf("unexpected file.\n--- got ---\n%s\n--- want ---\n%s", got, want)
		}
	})
	t.Run("execute guardrail", func(t *testing.T) {
		dir := t.TempDir()
		path := writeSDLCFile(t, dir, "config.toml",
			"[execute.guardrails.a]\nseverity = \"error\"\n\n[execute.guardrails.b]\nseverity = \"warning\"\n")

		res, text := callRegisteredSetupWriteSectionsIn(t, dir, `{"execute.guardrails.a":null}`)
		if res.IsError {
			t.Fatalf("expected success, got tool error:\n%s", text)
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if want := "[execute.guardrails.b]\nseverity = \"warning\"\n"; string(got) != want {
			t.Errorf("unexpected file.\n--- got ---\n%s\n--- want ---\n%s", got, want)
		}
	})
	t.Run("write and delete in one call", func(t *testing.T) {
		dir := t.TempDir()
		path := writeSDLCFile(t, dir, "config.toml", guardrailsFile)

		res, text := callRegisteredSetupWriteSectionsIn(t, dir,
			`{"plan.guardrails.dry":{"description":"No duplicate logic.","severity":"error"},"plan.guardrails.dry-old":null}`)
		if res.IsError {
			t.Fatalf("expected success, got tool error:\n%s", text)
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		// The write of plan.guardrails.dry restores the shipped jira tip
		// (whole-file tip restore), so pin the guardrail blocks only.
		want := "[plan.guardrails.dry]\ndescription = \"No duplicate logic.\"\nseverity = 'error'\n\n[jira]\n"
		if !strings.HasPrefix(string(got), want) {
			t.Errorf("unexpected file start.\n--- got ---\n%s\n--- want prefix ---\n%s", got, want)
		}
		if strings.Contains(string(got), "dry-old") {
			t.Errorf("plan.guardrails.dry-old survived the delete:\n%s", got)
		}
		for _, want := range []string{"- written:\n  - plan.guardrails.dry\n- root:", "## deleted[0]\n- id: plan.guardrails.dry-old\n"} {
			if !strings.Contains(text, want) {
				t.Errorf("result missing %q:\n%s", want, text)
			}
		}
	})
	// A layout the in-place delete cannot edit, in a file with no comment
	// line, is rewritten whole: the delete reports the id as deleted and
	// warns about the rewrite, as a write does.
	t.Run("rewrite fallback warns", func(t *testing.T) {
		dir := t.TempDir()
		path := writeSDLCFile(t, dir, "config.toml",
			"[plan.guardrails]\nx = { severity = \"error\" }\ny = { severity = \"warning\" }\n")

		out, err := setupWriteSections(dir, dir, SetupWriteSectionsIn{SectionsJSON: `{"plan.guardrails.x":null}`})
		if err != nil {
			t.Fatalf("setupWriteSections: %v", err)
		}
		if !out.OK || len(out.Written) != 0 || len(out.Deleted) != 1 || out.Deleted[0].ID != "plan.guardrails.x" ||
			out.Deleted[0].Table["severity"] != "error" {
			t.Errorf("out = %+v, want ok, nothing written, and plan.guardrails.x deleted with its data", out)
		}
		wantWarning := "Section plan.guardrails.x: could not edit .sdlc-v2/config.toml in place; the file had no comments, so it was rewritten and template tips were added"
		if !slices.Contains(out.Warnings, wantWarning) {
			t.Errorf("warnings = %q, want %q", out.Warnings, wantWarning)
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(got), "x = ") || !strings.Contains(string(got), "warning") {
			t.Errorf("rewritten file must hold y and not x:\n%s", got)
		}
	})
}

// TestSetupWriteSections_NullOnNonGuardrailStillClears pins that the delete
// is limited to guardrail ids: a null on plan.guardrails itself (not a
// guardrail id) still writes an empty table, as before.
func TestSetupWriteSections_NullOnNonGuardrailStillClears(t *testing.T) {
	dir := t.TempDir()
	path := writeSDLCFile(t, dir, "config.toml", "[plan.guardrails.a]\nseverity = \"error\"\n\n[jira]\ndefaultProject = \"P\"\n")

	res, text := callRegisteredSetupWriteSectionsIn(t, dir, `{"plan.guardrails":null}`)
	if res.IsError {
		t.Fatalf("expected success, got tool error:\n%s", text)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), "plan.guardrails.a") {
		t.Errorf("plan.guardrails.a survived a null on plan.guardrails:\n%s", got)
	}
	if !strings.Contains(string(got), "[plan.guardrails]") {
		t.Errorf("plan.guardrails was deleted, want an empty table:\n%s", got)
	}
}

// TestSetupWriteSections_NullOnMissingGuardrail verifies that a delete with
// nothing to delete returns ok, names the id in the warnings, sets next, and
// leaves the file unchanged (or missing).
func TestSetupWriteSections_NullOnMissingGuardrail(t *testing.T) {
	t.Run("table not in the file", func(t *testing.T) {
		dir := t.TempDir()
		path := writeSDLCFile(t, dir, "config.toml", guardrailsFile)

		res, text := callRegisteredSetupWriteSectionsIn(t, dir, `{"plan.guardrails.x":null}`)
		if res.IsError {
			t.Fatalf("expected success, got tool error:\n%s", text)
		}
		for _, want := range []string{
			"- ok: true",
			"plan.guardrails.x: not present, nothing deleted",
			"**Next:**",
			"Nothing was deleted for plan.guardrails.x",
		} {
			if !strings.Contains(text, want) {
				t.Errorf("missing %q:\n%s", want, text)
			}
		}
		if strings.Contains(text, "  - plan.guardrails.x") {
			t.Errorf("plan.guardrails.x is listed as written:\n%s", text)
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != guardrailsFile {
			t.Errorf("file changed:\n%s", got)
		}
	})
	t.Run("no config file", func(t *testing.T) {
		res, text, dir := callRegisteredSetupWriteSections(t, `{"execute.guardrails.x":null}`)
		if res.IsError {
			t.Fatalf("expected success, got tool error:\n%s", text)
		}
		if !strings.Contains(text, "execute.guardrails.x: not present, nothing deleted") || !strings.Contains(text, "- ok: true") {
			t.Errorf("unexpected result:\n%s", text)
		}
		if _, err := os.Stat(filepath.Join(dir, ".sdlc-v2", "config.toml")); !os.IsNotExist(err) {
			t.Errorf("config.toml was created (stat err: %v)", err)
		}
	})
}

// TestSetupWriteSections_NullGuardrailWithComment verifies that a guardrail
// table that holds a comment line is not deleted: the call reports the
// comment refusal and next names the hand-edit instruction. The file is unchanged.
func TestSetupWriteSections_NullGuardrailWithComment(t *testing.T) {
	dir := t.TempDir()
	content := "[plan.guardrails.dry]\n# keep this note\nseverity = \"warning\"\n\n[jira]\ndefaultProject = \"P\"\n"
	path := writeSDLCFile(t, dir, "config.toml", content)

	res, text := callRegisteredSetupWriteSectionsIn(t, dir, `{"plan.guardrails.dry":null}`)
	if res.IsError {
		t.Fatalf("expected a tool success carrying a per-section error, got tool error:\n%s", text)
	}
	for _, want := range []string{
		"- ok: false",
		"section plan.guardrails.dry:",
		"Edit section plan.guardrails.dry in .sdlc-v2/config.toml by hand, then run setup again for that section.",
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
		t.Errorf("file changed despite the refusal:\n%s", got)
	}
}

// TestSetupWriteSections_NullGuardrailTargetUserRejected verifies that a
// null guardrail id with target "user" returns a DomainError, writes nothing
// (not even the other sections of the call), and leaves config.toml intact.
func TestSetupWriteSections_NullGuardrailTargetUserRejected(t *testing.T) {
	dir := t.TempDir()
	path := writeSDLCFile(t, dir, "config.toml", guardrailsFile)
	userPath := filepath.Join(t.TempDir(), "user-local.toml")
	t.Setenv(config.UserConfigPathEnv, userPath)

	res, text := callRegisteredSetupWriteSectionsInArgs(t, dir, map[string]any{
		"sectionsJson": `{"plan.guardrails.dry-old":null,"style":{"foo":"bar"}}`,
		"target":       "user",
	})
	if !res.IsError {
		t.Fatalf("expected a tool error, got success:\n%s", text)
	}
	for _, want := range []string{
		"guardrail ids live in config.toml",
		"plan.guardrails.dry-old",
		"send the null with target project",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q:\n%s", want, text)
		}
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != guardrailsFile {
		t.Errorf("config.toml changed:\n%s", got)
	}
	if _, err := os.Stat(userPath); !os.IsNotExist(err) {
		t.Errorf("user config file was written (stat err: %v); want no write", err)
	}

	// Several null guardrail ids, from both sections: the message lists
	// every one, in sorted order.
	content := guardrailsFile + "\n[execute.guardrails.b]\nseverity = \"warning\"\n"
	path = writeSDLCFile(t, dir, "config.toml", content)
	res, text = callRegisteredSetupWriteSectionsInArgs(t, dir, map[string]any{
		"sectionsJson": `{"plan.guardrails.dry-old":null,"plan.guardrails.dry":null,"execute.guardrails.b":null,"style":{"foo":"bar"}}`,
		"target":       "user",
	})
	if !res.IsError {
		t.Fatalf("expected a tool error, got success:\n%s", text)
	}
	if want := "[execute.guardrails.b plan.guardrails.dry plan.guardrails.dry-old] cannot be deleted"; !strings.Contains(text, want) {
		t.Errorf("missing %q:\n%s", want, text)
	}
	if got, _ := os.ReadFile(path); string(got) != content {
		t.Errorf("config.toml changed:\n%s", got)
	}
	if _, err := os.Stat(userPath); !os.IsNotExist(err) {
		t.Errorf("user config file was written (stat err: %v); want no write", err)
	}
}

// nextLine returns the "**Next:**" line of a rendered tool result, or "" when
// the result has none.
func nextLine(text string) string {
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "**Next:**") {
			return line
		}
	}
	return ""
}

// commentedGuardrailBlock is a plan guardrail block that holds a comment
// line, so a delete of it is refused.
const commentedGuardrailBlock = "[plan.guardrails.dry]\n# keep this note\nseverity = \"warning\"\n"

// TestSetupWriteSections_RefusedGuardrailDeleteAmongWrites pins what a call
// leaves behind when the delete of a commented guardrail fails after, before
// or between writes of other sections. Ids run in sorted order, so the ids
// below place the delete first, last and in the middle. In each case the call
// reports ok false, the refused block stays in config.toml byte for byte,
// every other section is written, and next ends with the sentence that says
// other sections were written.
func TestSetupWriteSections_RefusedGuardrailDeleteAmongWrites(t *testing.T) {
	const deleteID = "plan.guardrails.dry"
	const wantNextSuffix = "then run setup again for that section. Other sections were written."
	tests := []struct {
		name        string
		sections    string
		wantWritten []string // other sections of the call, listed as written
		wantInTOML  []string // text config.toml must hold afterwards
		wantInLocal []string // text local.toml must hold afterwards
	}{
		{
			name:        "delete sorted first, write after it",
			sections:    `{"plan.guardrails.dry":null,"style":{"foo":"bar"}}`,
			wantWritten: []string{"style"},
			wantInLocal: []string{"foo"},
		},
		{
			name:        "delete sorted last, write before it",
			sections:    `{"commit":{"style":"conventional"},"plan.guardrails.dry":null}`,
			wantWritten: []string{"commit"},
			wantInTOML:  []string{"style = 'conventional'"},
		},
		{
			name:        "delete between two writes",
			sections:    `{"commit":{"style":"conventional"},"plan.guardrails.dry":null,"style":{"foo":"bar"}}`,
			wantWritten: []string{"commit", "style"},
			wantInTOML:  []string{"style = 'conventional'"},
			wantInLocal: []string{"foo"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			content := commentedGuardrailBlock + "\n[jira]\ndefaultProject = \"P\"\n"
			path := writeSDLCFile(t, dir, "config.toml", content)

			res, text := callRegisteredSetupWriteSectionsIn(t, dir, tt.sections)
			if res.IsError {
				t.Fatalf("expected a tool success carrying a per-section error, got tool error:\n%s", text)
			}
			for _, want := range []string{"- ok: false", "section " + deleteID + ":", "holds a comment line"} {
				if !strings.Contains(text, want) {
					t.Errorf("output missing %q:\n%s", want, text)
				}
			}
			for _, id := range tt.wantWritten {
				if !strings.Contains(text, "  - "+id+"\n") {
					t.Errorf("output does not list %s as written:\n%s", id, text)
				}
			}
			if strings.Contains(text, "  - "+deleteID+"\n") {
				t.Errorf("output lists the refused %s as written:\n%s", deleteID, text)
			}
			if next := nextLine(text); !strings.HasPrefix(next, "**Next:** Edit section "+deleteID+" in .sdlc-v2/config.toml by hand, ") || !strings.HasSuffix(next, wantNextSuffix) {
				t.Errorf("next line = %q, want it to start with the hand-edit step and end with %q", next, wantNextSuffix)
			}

			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(got), commentedGuardrailBlock) {
				t.Errorf("the commented guardrail block did not survive:\n%s", got)
			}
			if len(tt.wantInTOML) == 0 && string(got) != content {
				t.Errorf("config.toml changed although no project section was written:\n%s", got)
			}
			for _, want := range tt.wantInTOML {
				if !strings.Contains(string(got), want) {
					t.Errorf("config.toml missing %q:\n%s", want, got)
				}
			}
			for _, want := range tt.wantInLocal {
				local, err := os.ReadFile(filepath.Join(dir, ".sdlc-v2", "local.toml"))
				if err != nil {
					t.Fatalf("read local.toml: %v", err)
				}
				if !strings.Contains(string(local), want) {
					t.Errorf("local.toml missing %q:\n%s", want, local)
				}
			}
		})
	}
}

// TestSetupWriteSections_RefusedAndMissingGuardrailShareNext verifies that a
// refused delete and a delete of a missing table in one call both reach the
// output: the refusal in errors, the missing id in warnings, and one next that
// holds the hand-edit instruction followed by the note on the missing id. A call with
// two missing ids joins them in one note.
func TestSetupWriteSections_RefusedAndMissingGuardrailShareNext(t *testing.T) {
	t.Run("refused and missing", func(t *testing.T) {
		dir := t.TempDir()
		content := commentedGuardrailBlock + "\n[jira]\ndefaultProject = \"P\"\n"
		path := writeSDLCFile(t, dir, "config.toml", content)

		res, text := callRegisteredSetupWriteSectionsIn(t, dir, `{"plan.guardrails.dry":null,"plan.guardrails.zz":null}`)
		if res.IsError {
			t.Fatalf("expected a tool success carrying a per-section error, got tool error:\n%s", text)
		}
		for _, want := range []string{
			"- ok: false",
			"section plan.guardrails.dry:",
			"plan.guardrails.zz: not present, nothing deleted",
		} {
			if !strings.Contains(text, want) {
				t.Errorf("output missing %q:\n%s", want, text)
			}
		}
		wantNext := "**Next:** Edit section plan.guardrails.dry in .sdlc-v2/config.toml by hand, then run setup again for that section. Other sections were written." +
			" Nothing was deleted for plan.guardrails.zz: no such guardrail table. Check the id if you expected a change."
		if next := nextLine(text); next != wantNext {
			t.Errorf("next line = %q, want %q", next, wantNext)
		}
		if got, _ := os.ReadFile(path); string(got) != content {
			t.Errorf("config.toml changed:\n%s", got)
		}
	})
	t.Run("two missing ids", func(t *testing.T) {
		dir := t.TempDir()
		path := writeSDLCFile(t, dir, "config.toml", guardrailsFile)

		res, text := callRegisteredSetupWriteSectionsIn(t, dir, `{"plan.guardrails.x":null,"execute.guardrails.y":null}`)
		if res.IsError {
			t.Fatalf("expected success, got tool error:\n%s", text)
		}
		for _, want := range []string{
			"- ok: true",
			"execute.guardrails.y: not present, nothing deleted",
			"plan.guardrails.x: not present, nothing deleted",
		} {
			if !strings.Contains(text, want) {
				t.Errorf("output missing %q:\n%s", want, text)
			}
		}
		wantNext := "**Next:** Nothing was deleted for execute.guardrails.y, plan.guardrails.x: no such guardrail table. Check the id if you expected a change."
		if next := nextLine(text); next != wantNext {
			t.Errorf("next line = %q, want %q", next, wantNext)
		}
		if got, _ := os.ReadFile(path); string(got) != guardrailsFile {
			t.Errorf("config.toml changed:\n%s", got)
		}
	})
}

// TestSetupWriteSections_NullGuardrailDeleteErrorWithoutCommentRefusal covers
// a delete that fails for a reason other than a comment: config.toml does not
// parse. The error is reported for the id, next carries no hand-edit instruction
// because the failure is not a comment refusal, another section of the call is
// still written, and config.toml is not changed.
func TestSetupWriteSections_NullGuardrailDeleteErrorWithoutCommentRefusal(t *testing.T) {
	dir := t.TempDir()
	content := "[plan.guardrails.dry\nseverity = \"warning\"\n"
	path := writeSDLCFile(t, dir, "config.toml", content)

	res, text := callRegisteredSetupWriteSectionsIn(t, dir, `{"plan.guardrails.dry":null,"style":{"foo":"bar"}}`)
	if res.IsError {
		t.Fatalf("expected a tool success carrying a per-section error, got tool error:\n%s", text)
	}
	for _, want := range []string{"- ok: false", "section plan.guardrails.dry:", "config.toml", "  - style\n"} {
		if !strings.Contains(text, want) {
			t.Errorf("output missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "holds a comment line") {
		t.Errorf("output reports a comment refusal for a parse error:\n%s", text)
	}
	if strings.Contains(text, "  - plan.guardrails.dry\n") {
		t.Errorf("output lists the failed delete as written:\n%s", text)
	}
	if next := nextLine(text); next != "" {
		t.Errorf("next line = %q, want none: a parse error is not a comment refusal", next)
	}
	if got, _ := os.ReadFile(path); string(got) != content {
		t.Errorf("config.toml changed:\n%s", got)
	}
	local, err := os.ReadFile(filepath.Join(dir, ".sdlc-v2", "local.toml"))
	if err != nil || !strings.Contains(string(local), "foo") {
		t.Errorf("local.toml = %q (err %v), want the style section written", local, err)
	}
}
