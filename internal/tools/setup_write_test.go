// Package tools: tests for setup_write_sections's scaffold_ci auto-trigger
// (Task 10 addition).
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
// cannot edit in place (the section inside an inline table) is still written
// correctly, and that the tool warns that the file's comments were removed.
func TestSetupWriteSections_FallbackWarns(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".sdlc-v2", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("# doc\nplan = { tasks = { note = \"old\" } }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, text := callRegisteredSetupWriteSectionsIn(t, dir, `{"plan.tasks":{"note":"new"}}`)
	if res.IsError {
		t.Fatalf("expected success, got tool error:\n%s", text)
	}
	if !strings.Contains(text, "section plan.tasks: could not edit .sdlc-v2/config.toml in place") {
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
			newBlock: "[commit]\nallowedScopes = ['api']\nallowedTypes = ['feat', 'fix']\n",
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
			if strings.Contains(text, "comments were removed") {
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

	out, err := setupWriteSections(root, SetupWriteSectionsIn{
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

	if len(out.Scaffold) != 10 {
		t.Fatalf("expected 10 scaffold file reports, got %d", len(out.Scaffold))
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

	out, err := setupWriteSections(root, SetupWriteSectionsIn{
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

	if _, err := setupWriteSections(root, SetupWriteSectionsIn{
		SectionsJSON: `{"version":{"tag.enabled":true,"tag.prefix":"v"}}`,
	}); err != nil {
		t.Fatalf("setupWriteSections (first): %v", err)
	}

	out, err := setupWriteSections(root, SetupWriteSectionsIn{
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
	if strings.Contains(text, "comments were removed") {
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
