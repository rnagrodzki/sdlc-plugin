package config

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	version "github.com/rnagrodzki/sdlc-plugin"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
)

// writeSpliceFixture seeds .sdlc-v2/<file> with content, writes section name
// through WriteSectionReport and returns the new file text and whether the
// write fell back to a full rewrite.
func writeSpliceFixture(t *testing.T, file, content, name string, v map[string]any) (string, bool) {
	t.Helper()
	resetTrace()
	Quiet = true
	t.Cleanup(func() { Quiet = false })
	root := t.TempDir()
	path := filepath.Join(root, paths.DataDir, file)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	rewrote, err := WriteSectionReport(root, name, v)
	if err != nil {
		t.Fatalf("WriteSectionReport(%q): %v", name, err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Round trip: the section must decode to exactly the written value.
	raw := readRawTOML(t, root, file)
	if sec := extractSectionPath(raw, name); !reflect.DeepEqual(sec, v) {
		t.Errorf("decoded %s = %#v, want %#v", name, sec, v)
	}
	return string(got), rewrote
}

func TestWriteSection_SpliceCases(t *testing.T) {
	cases := []struct {
		name    string
		file    string
		content string
		section string
		value   map[string]any
		want    string
	}{
		{
			name:    "absent section appended after one blank line",
			file:    "config.toml",
			content: "# head\n[commit]\n# keep me\nallowedTypes = [\"feat\"]\n\n\n",
			section: "jira",
			value:   map[string]any{"defaultProject": "PROJ"},
			want:    "# head\n[commit]\n# keep me\nallowedTypes = [\"feat\"]\n\n[jira]\ndefaultProject = 'PROJ'\n",
		},
		{
			name:    "absent section appended to a file without a final newline",
			file:    "config.toml",
			content: "[commit]\nallowedTypes = [\"feat\"]",
			section: "jira",
			value:   map[string]any{"defaultProject": "PROJ"},
			want:    "[commit]\nallowedTypes = [\"feat\"]\n\n[jira]\ndefaultProject = 'PROJ'\n",
		},
		{
			name: "table replaced in place, comments above and after it kept",
			file: "config.toml",
			content: "# jira doc\n[jira]\n# lost: inside the block\ndefaultProject = \"OLD\" # lost: same line\n" +
				"# kept: after the last key\n\n# commit doc\n[commit]\nallowedTypes = []\n",
			section: "jira",
			value:   map[string]any{"defaultProject": "NEW"},
			want: "# jira doc\n[jira]\n# lost: inside the block\ndefaultProject = 'NEW' # lost: same line\n" +
				"# kept: after the last key\n\n# commit doc\n[commit]\nallowedTypes = []\n",
		},
		{
			name: "dotted leaf keeps the parent table and its siblings",
			file: "config.toml",
			content: "[plan]\n# plan doc\n\n[plan.tasks]\nnote = \"old\"\n\n" +
				"[plan.guardrails.g1]\nseverity = \"error\"\n",
			section: "plan.tasks",
			value:   map[string]any{"note": "new"},
			want: "[plan]\n# plan doc\n\n[plan.tasks]\nnote = 'new'\n\n" +
				"[plan.guardrails.g1]\nseverity = \"error\"\n",
		},
		{
			name: "all sub-tables replaced as one unit, even when not adjacent",
			file: "config.toml",
			content: "[plan.guardrails.a]\nseverity = \"error\"\n\n# jira doc\n[jira]\ndefaultProject = \"P\"\n\n" +
				"[plan.guardrails.b]\nseverity = \"warning\"\n\n# tail\n",
			section: "plan.guardrails",
			value:   map[string]any{"c": map[string]any{"severity": "error"}},
			want:    "[plan.guardrails.c]\nseverity = 'error'\n\n# jira doc\n[jira]\ndefaultProject = \"P\"\n\n# tail\n",
		},
		{
			name: "array of tables replaced",
			file: "config.toml",
			content: "[[plan.guardrails]]\nid = \"a\"\n\n[[plan.guardrails]]\nid = \"b\"\n\n" +
				"[commit]\nallowedTypes = []\n",
			section: "plan.guardrails",
			value:   map[string]any{"a": map[string]any{"severity": "error"}},
			want:    "[plan.guardrails.a]\nseverity = 'error'\n\n[commit]\nallowedTypes = []\n",
		},
		{
			name:    "dotted key under the parent table is removed",
			file:    "config.toml",
			content: "[plan]\ntasks.note = \"old\"\nother = 1\n",
			section: "plan.tasks",
			value:   map[string]any{"note": "new"},
			want:    "[plan]\nother = 1\n\n[plan.tasks]\nnote = 'new'\n",
		},
		{
			name: "header-like text in strings, arrays and comments is not a header",
			file: "config.toml",
			content: "[plan]\nnote = \"\"\"\n[plan.tasks]\nnot a header\n\"\"\"\n" +
				"lit = '''\n[plan.tasks]\n'''\n" +
				"matrix = [\n  [\"a\"],\n  [\"b\"]\n]\n" +
				"# [plan.tasks]\n",
			section: "plan.tasks",
			value:   map[string]any{"note": "real"},
			want: "[plan]\nnote = \"\"\"\n[plan.tasks]\nnot a header\n\"\"\"\n" +
				"lit = '''\n[plan.tasks]\n'''\n" +
				"matrix = [\n  [\"a\"],\n  [\"b\"]\n]\n" +
				"# [plan.tasks]\n[plan.tasks]\nnote = 'real'\n",
		},
		{
			name:    "empty value writes an empty table",
			file:    "config.toml",
			content: "[plan.tasks]\nnote = \"x\"\n\n[jira]\ndefaultProject = \"P\"\n",
			section: "plan.tasks",
			value:   map[string]any{},
			want:    "[plan.tasks]\n\n[jira]\ndefaultProject = \"P\"\n",
		},
		{
			name:    "one changed key: only its value text changes",
			file:    "config.toml",
			content: "# jira doc\n[jira]\n# tip\n  defaultProject = \"OLD\"\n\n[commit]\nallowedTypes = []\n",
			section: "jira",
			value:   map[string]any{"defaultProject": "NEW"},
			want:    "# jira doc\n[jira]\n# tip\n  defaultProject = 'NEW'\n\n[commit]\nallowedTypes = []\n",
		},
		{
			name:    "no value change leaves the file byte-identical",
			file:    "local.toml",
			content: "[ship]\n  steps = [ \"execute\",\n    \"commit\" ]   # c\nbump = \"patch\"\n\n# tail\n",
			section: "ship",
			value:   map[string]any{"steps": []any{"execute", "commit"}, "bump": "patch"},
			want:    "[ship]\n  steps = [ \"execute\",\n    \"commit\" ]   # c\nbump = \"patch\"\n\n# tail\n",
		},
		{
			name:    "removed key loses its line, the comment above it stays",
			file:    "config.toml",
			content: "[jira]\ndefaultProject = \"P\"\n# site tip\nsite = \"s\"\n\n[commit]\nallowedTypes = []\n",
			section: "jira",
			value:   map[string]any{"defaultProject": "P"},
			want:    "[jira]\ndefaultProject = \"P\"\n# site tip\n\n[commit]\nallowedTypes = []\n",
		},
		{
			name: "removed sub-table loses header, multi-line value and one blank line",
			file: "config.toml",
			content: "[plan.guardrails.a]\nseverity = \"error\"\n\n# b doc\n[plan.guardrails.b]\n# inside b\n" +
				"description = \"\"\"\nmulti\nline\"\"\"\n\n[jira]\ndefaultProject = \"P\"\n",
			section: "plan.guardrails",
			value:   map[string]any{"a": map[string]any{"severity": "error"}},
			want:    "[plan.guardrails.a]\nseverity = \"error\"\n\n# b doc\n# inside b\n[jira]\ndefaultProject = \"P\"\n",
		},
		{
			name:    "table changed to array of tables at its first old block, below its comments",
			file:    "config.toml",
			content: "# rules doc\n[plan.guardrails.a]\nseverity = \"error\"\n\n[commit]\nallowedTypes = []\n",
			section: "plan",
			value:   map[string]any{"guardrails": []any{map[string]any{"id": "x"}}},
			want:    "# rules doc\n[[plan.guardrails]]\nid = 'x'\n\n[commit]\nallowedTypes = []\n",
		},
		{
			name:    "untouched keys keep their order, quote style and bytes",
			file:    "local.toml",
			content: "[ship]\nzeta = 'z'\nalpha   =   \"a\"\nbump = \"patch\"\n",
			section: "ship",
			value:   map[string]any{"zeta": "z", "alpha": "a", "bump": "minor"},
			want:    "[ship]\nzeta = 'z'\nalpha   =   \"a\"\nbump = 'minor'\n",
		},
		{
			name:    "new key goes after the last key of its table",
			file:    "config.toml",
			content: "[jira]\ndefaultProject = \"P\"\n# trailing comment\n\n[commit]\nallowedTypes = []\n",
			section: "jira",
			value:   map[string]any{"defaultProject": "P", "site": "x"},
			want:    "[jira]\ndefaultProject = \"P\"\nsite = 'x'\n# trailing comment\n\n[commit]\nallowedTypes = []\n",
		},
		{
			name:    "new sub-table goes after the last line of its section",
			file:    "config.toml",
			content: "[plan.tasks]\nnote = \"x\"\n\n[jira]\ndefaultProject = \"P\"\n",
			section: "plan.tasks",
			value:   map[string]any{"note": "x", "sub": map[string]any{"a": "b"}},
			want:    "[plan.tasks]\nnote = \"x\"\n\n[plan.tasks.sub]\na = 'b'\n\n[jira]\ndefaultProject = \"P\"\n",
		},
		{
			name:    "absent table goes after its commented header block",
			file:    "config.toml",
			content: "[commit]\nallowedTypes = []\n\n# Jira doc\n#  [ jira ]\n# defaultProject = \"X\"\n\n# tail\n",
			section: "jira",
			value:   map[string]any{"defaultProject": "P"},
			want:    "[commit]\nallowedTypes = []\n\n# Jira doc\n#  [ jira ]\n# defaultProject = \"X\"\n[jira]\ndefaultProject = 'P'\n\n# tail\n",
		},
		{
			name:    "changed single-line value keeps its trailing comment",
			file:    "config.toml",
			content: "[jira]\ndefaultProject = \"OLD\"   # the key\n",
			section: "jira",
			value:   map[string]any{"defaultProject": "NEW"},
			want:    "[jira]\ndefaultProject = 'NEW'   # the key\n",
		},
		{
			name:    "changed multi-line value becomes one line, trailing comment kept",
			file:    "config.toml",
			content: "[commit]\nallowedTypes = [\n  \"feat\",\n  \"fix\",\n] # types\nallowedScopes = []\n",
			section: "commit",
			value:   map[string]any{"allowedTypes": []any{"docs"}, "allowedScopes": []any{}},
			want:    "[commit]\nallowedTypes = ['docs'] # types\nallowedScopes = []\n",
		},
		{
			name:    "dotted keys inside the table are edited in place",
			file:    "config.toml",
			content: "[version]\n# tag doc\ntag.enabled = true\n",
			section: "version",
			value:   map[string]any{"tag": map[string]any{"enabled": false, "prefix": "v"}},
			want:    "[version]\n# tag doc\ntag.enabled = false\ntag.prefix = 'v'\n",
		},
		{
			name:    "missing file is created",
			file:    "local.toml",
			content: "",
			section: "ship",
			value:   map[string]any{"draft": true},
			want:    "[ship]\ndraft = true\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, rewrote := writeSpliceFixture(t, tc.file, tc.content, tc.section, tc.value)
			if rewrote {
				t.Errorf("fell back to a full rewrite")
			}
			if got != tc.want {
				t.Errorf("file text mismatch\n--- got ---\n%s\n--- want ---\n%s", got, tc.want)
			}
		})
	}
}

// TestWriteSection_SpliceTemplateVersion writes the whole version section
// into the shipped config.toml template: the version tables are replaced as
// one unit and everything outside them is unchanged.
func TestWriteSection_SpliceTemplateVersion(t *testing.T) {
	tmpl := version.ConfigTemplate
	value := map[string]any{
		"preRelease":       "beta",
		"preReleasePolicy": "never",
		"method":           "pr",
		"tag":              map[string]any{"enabled": true, "prefix": ""},
		"versionFile":      map[string]any{"enabled": false, "path": "", "fileType": ""},
		"changelog":        map[string]any{"enabled": true, "file": "CHANGELOG.md"},
	}
	got, rewrote := writeSpliceFixture(t, "config.toml", tmpl, "version", value)
	if rewrote {
		t.Fatalf("fell back to a full rewrite")
	}

	head := tmpl[:strings.Index(tmpl, "[version]\n")]
	tail := tmpl[strings.Index(tmpl, "# ─── Jira integration"):]
	if !strings.HasPrefix(got, head) {
		t.Errorf("text before [version] changed:\n%s", got)
	}
	if !strings.HasSuffix(got, tail) {
		t.Errorf("text after the version tables changed:\n%s", got)
	}
	// The commented-out pushAuth example sits after [version]'s last key, so
	// it is kept.
	if !strings.Contains(got, "# [version.pushAuth]\n# secretName = \"RELEASE_TOKEN\"\n") {
		t.Errorf("pushAuth comment lost:\n%s", got)
	}
	for _, header := range []string{"[version]\n", "[version.tag]\n", "[version.versionFile]\n", "[version.changelog]\n"} {
		if n := strings.Count(got, header); n != 1 {
			t.Errorf("%q appears %d times, want 1:\n%s", header, n, got)
		}
	}
	if strings.Contains(got, "\n\n\n") {
		t.Errorf("splice left a run of blank lines:\n%s", got)
	}
}

// TestWriteSection_SpliceTemplateLocal writes planStyle into the shipped
// local.toml template: [ship] and all its comments stay byte-for-byte.
func TestWriteSection_SpliceTemplateLocal(t *testing.T) {
	tmpl := version.LocalTemplate
	value := map[string]any{"audience": "executive"}
	got, rewrote := writeSpliceFixture(t, "local.toml", tmpl, "planStyle", value)
	if rewrote {
		t.Fatalf("fell back to a full rewrite")
	}
	head := tmpl[:strings.Index(tmpl, "[planStyle]\n")]
	if !strings.HasPrefix(got, head+"[planStyle]\naudience = 'executive'\n") {
		t.Errorf("local.toml: [ship] text changed or planStyle not spliced in place:\n%s", got)
	}
}

// TestWriteSection_SpliceRefusesToDropComments pins the refusal: when the
// section lives inside an inline table and the file has a comment line, the
// write returns ErrWouldDropComments and the file is not changed.
func TestWriteSection_SpliceRefusesToDropComments(t *testing.T) {
	resetTrace()
	Quiet = true
	t.Cleanup(func() { Quiet = false })
	root := t.TempDir()
	path := filepath.Join(root, paths.DataDir, "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	content := "# kept\nplan = { tasks = { note = \"old\" } }\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	rewrote, err := WriteSectionReport(root, "plan.tasks", map[string]any{"note": "new"})
	if !errors.Is(err, ErrWouldDropComments) {
		t.Fatalf("err = %v, want ErrWouldDropComments", err)
	}
	if rewrote {
		t.Errorf("rewrote = true, want false")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != content {
		t.Errorf("file changed:\n%s", got)
	}
}

// TestWriteSection_SpliceFallsBack pins the fallback: when the section lives
// inside an inline table and the file has no comment line, the file is
// rewritten from parsed data and the data is still correct.
func TestWriteSection_SpliceFallsBack(t *testing.T) {
	content := "plan = { tasks = { note = \"old\" } }\n"
	got, rewrote := writeSpliceFixture(t, "config.toml", content, "plan.tasks", map[string]any{"note": "new"})
	if !rewrote {
		t.Errorf("rewrote = false, want true for an inline-table layout")
	}
	if !strings.Contains(got, "note = 'new'") {
		t.Errorf("full rewrite did not write the value:\n%s", got)
	}
}

// TestCommentedHeaderEnd pins the commented-header lookup: any spacing
// inside the comment matches, the end is the line after the comment block,
// and a header for another path or an uncommented header does not match.
func TestCommentedHeaderEnd(t *testing.T) {
	lines := bytes.SplitAfter([]byte("[a]\nk = 1\n\n# doc\n#[ x . y ]\n# k = 2\n\n# [x]\n"), []byte("\n"))
	if got := commentedHeaderEnd(lines, []string{"x", "y"}); got != 6 {
		t.Errorf("x.y: got %d, want 6", got)
	}
	if got := commentedHeaderEnd(lines, []string{"x"}); got != 8 {
		t.Errorf("x: got %d, want 8", got)
	}
	if got := commentedHeaderEnd(lines, []string{"a"}); got != -1 {
		t.Errorf("a: got %d, want -1", got)
	}
}

// TestWriteSection_FallbackKeepsIntegers pins that the full-rewrite fallback
// writes whole numbers in the other sections as TOML integers. fsx.ReadTOML
// decodes every number as float64, so without a conversion "60" became
// "60.0" in every section the write did not touch.
func TestWriteSection_FallbackKeepsIntegers(t *testing.T) {
	content := "workspace = { tasks = { note = \"old\" } }\n\n[ship]\nexecuteWaveInterval = 60\nsteps = [\"execute\"]\n"
	got, rewrote := writeSpliceFixture(t, "local.toml", content, "workspace.tasks", map[string]any{"note": "new"})
	if !rewrote {
		t.Fatalf("rewrote = false, want true for an inline-table layout")
	}
	if !strings.Contains(got, "executeWaveInterval = 60\n") || strings.Contains(got, "60.0") {
		t.Errorf("fallback rewrite changed the [ship] integer into a float:\n%s", got)
	}
}
