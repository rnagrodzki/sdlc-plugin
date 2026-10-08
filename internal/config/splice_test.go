package config

import (
	"errors"
	"fmt"
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
			want: "# head\n[commit]\n# keep me\nallowedTypes = [\"feat\"]\n\n[jira]\n" +
				"# Default Jira project key (2–10 uppercase letters, e.g. \"PROJ\").\ndefaultProject = 'PROJ'\n",
		},
		{
			name:    "absent section appended to a file without a final newline",
			file:    "config.toml",
			content: "[commit]\n# own comment\nallowedTypes = [\"feat\"]",
			section: "jira",
			value:   map[string]any{"defaultProject": "PROJ"},
			want: "[commit]\n# own comment\nallowedTypes = [\"feat\"]\n\n[jira]\n" +
				"# Default Jira project key (2–10 uppercase letters, e.g. \"PROJ\").\ndefaultProject = 'PROJ'\n",
		},
		{
			name: "table replaced in place, comments above and after it kept",
			file: "config.toml",
			content: "# jira doc\n[jira]\n# lost: inside the block\ndefaultProject = \"OLD\" # lost: same line\n" +
				"# kept: after the last key\n\n# commit doc\n[commit]\n# own comment\nallowedTypes = []\n",
			section: "jira",
			value:   map[string]any{"defaultProject": "NEW"},
			want: "# jira doc\n[jira]\n# lost: inside the block\ndefaultProject = 'NEW' # lost: same line\n" +
				"# kept: after the last key\n\n# commit doc\n[commit]\n# own comment\nallowedTypes = []\n",
		},
		{
			// plan.tasks keeps its own leading comment here: the shipped
			// config.toml template has a commented [plan.tasks] example, so a
			// live plan.tasks header with no comment of its own would get
			// that template tip restored above it (tips.go's whole-file
			// restore) — unrelated to what this case pins (a dotted leaf
			// under [plan]).
			name: "dotted leaf keeps the parent table and its siblings",
			file: "config.toml",
			content: "[plan]\n# plan doc\n\n# tasks doc\n[plan.tasks]\nnote = \"old\"\n\n" +
				"[plan.guardrails.g1]\nseverity = \"error\"\n",
			section: "plan.tasks",
			value:   map[string]any{"note": "new"},
			want: "[plan]\n# plan doc\n\n# tasks doc\n[plan.tasks]\nnote = 'new'\n\n" +
				"[plan.guardrails.g1]\nseverity = \"error\"\n",
		},
		{
			name: "all sub-tables replaced as one unit, even when not adjacent",
			file: "config.toml",
			content: "[plan.guardrails.a]\nseverity = \"error\"\n\n# jira doc\n[jira]\n# own comment\ndefaultProject = \"P\"\n\n" +
				"[plan.guardrails.b]\nseverity = \"warning\"\n\n# tail\n",
			section: "plan.guardrails",
			value:   map[string]any{"c": map[string]any{"severity": "error"}},
			want:    "[plan.guardrails.c]\nseverity = 'error'\n\n# jira doc\n[jira]\n# own comment\ndefaultProject = \"P\"\n\n# tail\n",
		},
		{
			name: "array of tables replaced",
			file: "config.toml",
			content: "[[plan.guardrails]]\nid = \"a\"\n\n[[plan.guardrails]]\nid = \"b\"\n\n" +
				"[commit]\n# own comment\nallowedTypes = []\n",
			section: "plan.guardrails",
			value:   map[string]any{"a": map[string]any{"severity": "error"}},
			want:    "[plan.guardrails.a]\nseverity = 'error'\n\n[commit]\n# own comment\nallowedTypes = []\n",
		},
		{
			// The new [plan.tasks] header springs into existence here (the
			// old data was a dotted key), so there is no file position to
			// attach a test-owned comment to; the shipped config.toml
			// template's commented [plan.tasks] tip is restored above it,
			// same as "absent section appended" above.
			name:    "dotted key under the parent table is removed",
			file:    "config.toml",
			content: "[plan]\ntasks.note = \"old\"\nother = 1\n",
			section: "plan.tasks",
			value:   map[string]any{"note": "new"},
			want: "[plan]\nother = 1\n\n" +
				"# Team contract for plan tasks and deliverables (optional).\n[plan.tasks]\nnote = 'new'\n",
		},
		{
			name: "header-like text in strings and arrays is not a header, the commented one becomes live",
			file: "config.toml",
			content: "[plan]\nnote = \"\"\"\n[plan.tasks]\nnot a header\n\"\"\"\n" +
				"lit = '''\n[plan.tasks]\n'''\n" +
				"matrix = [\n  [\"a\"],\n  [\"b\"]\n]\n" +
				// Own comment directly above the commented header, so once
				// it is uncommented it already has a comment above it and
				// the shipped template's plan.tasks tip is not restored.
				"# own tip\n# [plan.tasks]\n",
			section: "plan.tasks",
			value:   map[string]any{"note": "real"},
			// Only the commented header is taken for plan.tasks: it becomes live.
			want: "[plan]\nnote = \"\"\"\n[plan.tasks]\nnot a header\n\"\"\"\n" +
				"lit = '''\n[plan.tasks]\n'''\n" +
				"matrix = [\n  [\"a\"],\n  [\"b\"]\n]\n" +
				"# own tip\n[plan.tasks]\nnote = 'real'\n",
		},
		{
			name:    "empty value writes an empty table",
			file:    "config.toml",
			content: "# tasks doc\n[plan.tasks]\nnote = \"x\"\n\n[jira]\n# own comment\ndefaultProject = \"P\"\n",
			section: "plan.tasks",
			value:   map[string]any{},
			want:    "# tasks doc\n[plan.tasks]\n\n[jira]\n# own comment\ndefaultProject = \"P\"\n",
		},
		{
			name:    "one changed key: only its value text changes",
			file:    "config.toml",
			content: "# jira doc\n[jira]\n# tip\n  defaultProject = \"OLD\"\n\n[commit]\n# own comment\nallowedTypes = []\n",
			section: "jira",
			value:   map[string]any{"defaultProject": "NEW"},
			want:    "# jira doc\n[jira]\n# tip\n  defaultProject = 'NEW'\n\n[commit]\n# own comment\nallowedTypes = []\n",
		},
		{
			name:    "no value change keeps key text byte-identical, missing tips still restored",
			file:    "local.toml",
			content: "[ship]\n  steps = [ \"execute\",\n    \"commit\" ]   # c\nbump = \"patch\"\n\n# tail\n",
			section: "ship",
			value:   map[string]any{"steps": []any{"execute", "commit"}, "bump": "patch"},
			want: "[ship]\n" +
				"# Pipeline steps for /ship: true runs the step, false skips it.\n" +
				"# The plugin fixes the order: \"execute\" | \"commit\" | \"review\" | \"verify-openspec\" |\n" +
				"#   \"archive-openspec\" | \"harden\" | \"pr\" | \"verify-pipeline\" |\n" +
				"#   \"await-remote-review\" | \"learnings-commit\"\n" +
				"# A step you do not list uses its default.\n" +
				"  steps = [ \"execute\",\n    \"commit\" ]   # c\n" +
				"# Default version bump.\n" +
				"# Valid: \"major\" | \"minor\" | \"patch\", or a pre-release label such as \"rc\", \"beta\", \"alpha\"\n" +
				"bump = \"patch\"\n\n# tail\n",
		},
		{
			name:    "removed key loses its line, the comment above it stays",
			file:    "config.toml",
			content: "[jira]\ndefaultProject = \"P\"\n# site tip\nsite = \"s\"\n\n[commit]\n# own comment\nallowedTypes = []\n",
			section: "jira",
			value:   map[string]any{"defaultProject": "P"},
			want: "[jira]\n" +
				"# Default Jira project key (2–10 uppercase letters, e.g. \"PROJ\").\ndefaultProject = \"P\"\n" +
				"# site tip\n\n[commit]\n# own comment\nallowedTypes = []\n",
		},
		{
			name: "removed sub-table loses header, multi-line value and one blank line",
			file: "config.toml",
			content: "[plan.guardrails.a]\nseverity = \"error\"\n\n# b doc\n[plan.guardrails.b]\n# inside b\n" +
				"description = \"\"\"\nmulti\nline\"\"\"\n\n[jira]\n# own comment\ndefaultProject = \"P\"\n",
			section: "plan.guardrails",
			value:   map[string]any{"a": map[string]any{"severity": "error"}},
			want:    "[plan.guardrails.a]\nseverity = \"error\"\n\n# b doc\n# inside b\n[jira]\n# own comment\ndefaultProject = \"P\"\n",
		},
		{
			name:    "table changed to array of tables at its first old block, below its comments",
			file:    "config.toml",
			content: "# rules doc\n[plan.guardrails.a]\nseverity = \"error\"\n\n[commit]\n# own comment\nallowedTypes = []\n",
			section: "plan",
			value:   map[string]any{"guardrails": []any{map[string]any{"id": "x"}}},
			want:    "# rules doc\n[[plan.guardrails]]\nid = 'x'\n\n[commit]\n# own comment\nallowedTypes = []\n",
		},
		{
			name:    "untouched keys keep their order, quote style and bytes",
			file:    "local.toml",
			content: "[ship]\nzeta = 'z'\nalpha   =   \"a\"\nbump = \"patch\"\n",
			section: "ship",
			value:   map[string]any{"zeta": "z", "alpha": "a", "bump": "minor"},
			want: "[ship]\nzeta = 'z'\nalpha   =   \"a\"\n" +
				"# Default version bump.\n" +
				"# Valid: \"major\" | \"minor\" | \"patch\", or a pre-release label such as \"rc\", \"beta\", \"alpha\"\n" +
				"bump = 'minor'\n",
		},
		{
			name:    "new key goes after the last key of its table",
			file:    "config.toml",
			content: "[jira]\ndefaultProject = \"P\"\n# trailing comment\n\n[commit]\n# own comment\nallowedTypes = []\n",
			section: "jira",
			value:   map[string]any{"defaultProject": "P", "site": "x"},
			want: "[jira]\n" +
				"# Default Jira project key (2–10 uppercase letters, e.g. \"PROJ\").\ndefaultProject = \"P\"\n" +
				"site = 'x'\n# trailing comment\n\n[commit]\n# own comment\nallowedTypes = []\n",
		},
		{
			name:    "new sub-table goes after the last line of its section",
			file:    "config.toml",
			content: "# tasks doc\n[plan.tasks]\nnote = \"x\"\n\n[jira]\n# own comment\ndefaultProject = \"P\"\n",
			section: "plan.tasks",
			value:   map[string]any{"note": "x", "sub": map[string]any{"a": "b"}},
			want:    "# tasks doc\n[plan.tasks]\nnote = \"x\"\n\n[plan.tasks.sub]\na = 'b'\n\n[jira]\n# own comment\ndefaultProject = \"P\"\n",
		},
		{
			name:    "absent table takes over its commented header and example lines",
			file:    "config.toml",
			content: "[commit]\n# own comment\nallowedTypes = []\n\n# Jira doc\n#  [ jira ]\n# defaultProject = \"X\"\n\n# tail\n",
			section: "jira",
			value:   map[string]any{"defaultProject": "P"},
			want: "[commit]\n# own comment\nallowedTypes = []\n\n# Jira doc\n[ jira ]\n" +
				"# Default Jira project key (2–10 uppercase letters, e.g. \"PROJ\").\ndefaultProject = 'P'\n\n# tail\n",
		},
		{
			name:    "changed single-line value keeps its trailing comment",
			file:    "config.toml",
			content: "[jira]\ndefaultProject = \"OLD\"   # the key\n",
			section: "jira",
			value:   map[string]any{"defaultProject": "NEW"},
			want: "[jira]\n" +
				"# Default Jira project key (2–10 uppercase letters, e.g. \"PROJ\").\ndefaultProject = 'NEW'   # the key\n",
		},
		{
			name:    "changed multi-line value becomes one line, trailing comment kept",
			file:    "config.toml",
			content: "[commit]\nallowedTypes = [\n  \"feat\",\n  \"fix\",\n] # types\nallowedScopes = []\n",
			section: "commit",
			value:   map[string]any{"allowedTypes": []any{"docs"}, "allowedScopes": []any{}},
			want: "[commit]\n" +
				"# Allowed commit types (conventional-commit prefix before the colon).\n" +
				"allowedTypes = ['docs'] # types\n" +
				"# Allowed scopes (empty = any scope accepted).\n" +
				"allowedScopes = []\n",
		},
		{
			name:    "dotted keys inside the table are edited in place",
			file:    "config.toml",
			content: "[version]\n# tag doc\ntag.enabled = true\n",
			section: "version",
			value:   map[string]any{"tag": map[string]any{"enabled": false, "prefix": "v"}},
			want: "[version]\n# tag doc\ntag.enabled = false\n" +
				"# Tag prefix (e.g. \"v\" → \"v1.2.3\", \"\" → \"1.2.3\")\ntag.prefix = 'v'\n",
		},
		{
			name:    "missing file is created",
			file:    "local.toml",
			content: "",
			section: "ship",
			value:   map[string]any{"draft": true},
			want:    "[ship]\n# Create PR as draft.\ndraft = true\n",
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

// TestSplice_CommentedExamples pins how a new key takes over its commented
// example line, and a new section its commented header line. Each fixture
// keeps a comment line directly above every new key, so no template tip is
// inserted and the expected text shows only the splice.
func TestSplice_CommentedExamples(t *testing.T) {
	cases := []struct {
		name    string
		file    string
		content string
		section string
		value   map[string]any
		want    string
	}{
		{
			name: "new key replaces its commented example, trailing tip kept",
			file: "local.toml",
			content: "[style]\n# note\n# audience = \"functional\"      # technical | functional | executive | general | beginner\n" +
				"# tone = \"direct\"              # direct | neutral\n",
			section: "style",
			value:   map[string]any{"audience": "technical"},
			want: "[style]\n# note\naudience = 'technical'      # technical | functional | executive | general | beginner\n" +
				"# tone = \"direct\"              # direct | neutral\n",
		},
		{
			name:    "two examples for one key: the first one is used",
			file:    "local.toml",
			content: "[style]\n# note\n# tone = \"direct\"   # first\n# tone = \"neutral\"  # second\n",
			section: "style",
			value:   map[string]any{"tone": "neutral"},
			want:    "[style]\n# note\ntone = 'neutral'   # first\n# tone = \"neutral\"  # second\n",
		},
		{
			name:    "indented example keeps its indentation",
			file:    "local.toml",
			content: "[style]\n  # note\n  #   tone = \"direct\"\n",
			section: "style",
			value:   map[string]any{"tone": "neutral"},
			want:    "[style]\n  # note\n  tone = 'neutral'\n",
		},
		{
			name:    "comment inside a multi-line value is not an example",
			file:    "local.toml",
			content: "[ship]\n# steps doc\nsteps = [\n  \"execute\",\n# foo = false\n]\n# foo doc\n",
			section: "ship",
			value:   map[string]any{"steps": []any{"execute"}, "foo": true},
			want:    "[ship]\n# steps doc\nsteps = [\n  \"execute\",\n# foo = false\n]\nfoo = true\n# foo doc\n",
		},
		{
			name: "commented header and its example are uncommented, other examples stay",
			file: "local.toml",
			content: "[ship]\n# bump doc\nbump = \"patch\"\n\n# ─── Review\n# [review]\n# Which changes to include.\n" +
				"# scope = \"working\"\n# Max dimensions.\n# maxDimensions = 8\n\n# [receivedReview]\n# scope = \"x\"\n",
			section: "review",
			value:   map[string]any{"scope": "diff"},
			want: "[ship]\n# bump doc\nbump = \"patch\"\n\n# ─── Review\n[review]\n# Which changes to include.\n" +
				"scope = 'diff'\n# Max dimensions.\n# maxDimensions = 8\n\n# [receivedReview]\n# scope = \"x\"\n",
		},
		{
			// Own comment above [review] here: the shipped local.toml
			// template has a commented [review] example with its own
			// header tip, so a live review header with nothing above it
			// would get that tip restored — unrelated to what this case
			// pins (an example below the next commented header is unused).
			name:    "example below the next commented header is not used",
			file:    "local.toml",
			content: "# own review doc\n[review]\n# a\n# [other]\n# foo = \"x\"\n",
			section: "review",
			value:   map[string]any{"foo": "y"},
			want:    "# own review doc\n[review]\nfoo = 'y'\n# a\n# [other]\n# foo = \"x\"\n",
		},
		{
			name:    "new sub-table goes below the uncommented example line",
			file:    "local.toml",
			content: "# doc\n# [automation]\n# note\n# zz = \"b\"\n# tail\n",
			section: "automation",
			value:   map[string]any{"zz": "a", "sub": map[string]any{"k": true}},
			want:    "# doc\n[automation]\n# note\nzz = 'a'\n\n[automation.sub]\nk = true\n# tail\n",
		},
		{
			// The commented [review] header here is not eligible to be
			// taken over (a live key follows it before the next header), so
			// [review] is appended fresh — same as "absent section
			// appended" in TestWriteSection_SpliceCases, which also gets
			// the shipped template's tip above the newly appended header.
			name:    "commented header with a live key/value below it: section appended",
			file:    "local.toml",
			content: "# [review]\n# foo = \"x\"\nstray = true\n",
			section: "review",
			value:   map[string]any{"foo": "y"},
			want: "# [review]\n# foo = \"x\"\nstray = true\n\n" +
				"# ─── Review defaults (optional — delete if not using) ─────────────\n[review]\nfoo = 'y'\n",
		},
		{
			name:    "CRLF file: uncommented lines keep CRLF",
			file:    "local.toml",
			content: "# doc\r\n# [review]\r\n# c\r\n# foo = \"working\"   # tip\r\n",
			section: "review",
			value:   map[string]any{"foo": "diff"},
			want:    "# doc\r\n[review]\r\n# c\r\nfoo = 'diff'   # tip\r\n",
		},
		{
			name:    "new dotted key replaces its commented example",
			file:    "config.toml",
			content: "[version]\n# doc\ntag.enabled = true\n# prefix doc\n# tag.prefix = \"v\"   # p\n",
			section: "version",
			value:   map[string]any{"tag": map[string]any{"enabled": true, "prefix": "x"}},
			want:    "[version]\n# doc\ntag.enabled = true\n# prefix doc\ntag.prefix = 'x'   # p\n",
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
// local.toml template: [ship] and all its comments stay byte-for-byte, and
// the new audience key takes over the first commented audience example
// under [planStyle] (the deprecated one), keeping its trailing tip.
func TestWriteSection_SpliceTemplateLocal(t *testing.T) {
	tmpl := version.LocalTemplate
	value := map[string]any{"audience": "executive"}
	got, rewrote := writeSpliceFixture(t, "local.toml", tmpl, "planStyle", value)
	if rewrote {
		t.Fatalf("fell back to a full rewrite")
	}
	at := strings.Index(tmpl, "[planStyle]\n")
	want := tmpl[:at] + strings.Replace(tmpl[at:], "# audience = \"functional\"", "audience = 'executive'", 1)
	if got != want {
		t.Errorf("local.toml: text outside the planStyle audience line changed:\n%s", got)
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

// TestRefuseCommentDrop_NamesRealCause pins the refusal message for each
// cause of a failed in-place edit: a layout the splicer does not handle
// (errNoSplice) gets the layout diagnosis; any other cause (a failed safety
// check, or another splice error) is named as a plugin bug, with no layout
// diagnosis.
func TestRefuseCommentDrop_NamesRealCause(t *testing.T) {
	const layout = "array of tables, inline table, or a dotted key defines it"
	tests := []struct {
		name       string
		cause      error
		wantLayout bool
		wantText   string
	}{
		{"no splice", errNoSplice, true, layout},
		{"wrapped no splice", fmt.Errorf("edit: %w", errNoSplice), true, layout},
		{"safety check", errSpliceMismatch, false, errSpliceMismatch.Error()},
		{"other splice error", errors.New("boom"), false, "boom"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := refuseCommentDrop("plan.tasks", "/x/config.toml", tt.cause)
			if !errors.Is(err, ErrWouldDropComments) {
				t.Fatalf("err = %v, want ErrWouldDropComments", err)
			}
			msg := err.Error()
			if got := strings.Contains(msg, layout); got != tt.wantLayout {
				t.Errorf("layout diagnosis present = %v, want %v: %s", got, tt.wantLayout, msg)
			}
			if !strings.Contains(msg, tt.wantText) {
				t.Errorf("message does not name the cause %q: %s", tt.wantText, msg)
			}
			if !tt.wantLayout && !strings.Contains(msg, "bug in the sdlc plugin") {
				t.Errorf("message does not call a non-layout cause a plugin bug: %s", msg)
			}
			if !strings.Contains(msg, "Edit that section by hand, then run setup again") {
				t.Errorf("message lost the recovery step: %s", msg)
			}
		})
	}
}

// TestWriteFileSection_ReadErrorReturned pins the read guard: when the
// target path exists but cannot be read as a file (here it is a directory),
// the write returns that read error and writes nothing.
func TestWriteFileSection_ReadErrorReturned(t *testing.T) {
	resetTrace()
	Quiet = true
	t.Cleanup(func() { Quiet = false })
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	rewrote, err := WriteFileSection(path, "plan", map[string]any{"note": "new"})
	if err == nil {
		t.Fatal("err = nil, want the read error")
	}
	if rewrote {
		t.Errorf("rewrote = true, want false")
	}
	if !strings.Contains(err.Error(), "config: read "+path) {
		t.Errorf("err = %v, want it to name the read of %s", err, path)
	}
	if errors.Is(err, ErrWouldDropComments) {
		t.Errorf("err = %v, a read error must not be reported as ErrWouldDropComments", err)
	}
	if fi, statErr := os.Stat(path); statErr != nil || !fi.IsDir() {
		t.Errorf("target changed: stat = %v, %v", fi, statErr)
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

// TestSplice_ParseCommentedHeader pins the commented-header parser: any
// spacing inside the comment matches, and prose, a live header, an
// array-of-tables header and a key/value do not.
func TestSplice_ParseCommentedHeader(t *testing.T) {
	tests := []struct {
		line   string
		want   []string
		wantOK bool
	}{
		{"# [review]\n", []string{"review"}, true},
		{"#[ x . y ]\r\n", []string{"x", "y"}, true},
		{"  #   [workspace.branch]   # tip\n", []string{"workspace", "branch"}, true},
		{"[review]\n", nil, false},
		{"# [[plan.guardrails]]\n", nil, false},
		{"# scope = \"working\"\n", nil, false},
		{"# Deprecated — moved to [style]; still accepted here.\n", nil, false},
		{"# [review] [ship]\n", nil, false},
		{"#\n", nil, false},
	}
	for _, tt := range tests {
		got, ok := parseCommentedHeader([]byte(tt.line))
		if ok != tt.wantOK || !reflect.DeepEqual(got, tt.want) {
			t.Errorf("parseCommentedHeader(%q) = %v, %v; want %v, %v", tt.line, got, ok, tt.want, tt.wantOK)
		}
	}
}

// TestSplice_ParseCommentedExample pins the commented-example parser: the
// key with dotted parts joined, the trailing tip with the blanks before it,
// and no match for prose, headers, live lines or multi-line values.
func TestSplice_ParseCommentedExample(t *testing.T) {
	tests := []struct {
		line   string
		key    string
		tip    string
		wantOK bool
	}{
		{"# audience = \"functional\"      # technical | functional\n", "audience", "      # technical | functional", true},
		{"# language = \"English\"\n", "language", "", true},
		{"  #tag.enabled=true   \r\n", "tag.enabled", "", true},
		{"# note = \"a # b\"  # real tip", "note", "  # real tip", true},
		{"# technicalTerms = []          # words\n", "technicalTerms", "          # words", true},
		{"audience = \"x\"\n", "", "", false},
		{"# Valid: \"major\" | \"minor\"\n", "", "", false},
		{"# [review]\n", "", "", false},
		{"# steps = [\n", "", "", false},
		{"# a = 1 b = 2\n", "", "", false},
		{"# Default version bump.\n", "", "", false},
	}
	for _, tt := range tests {
		key, tip, ok := parseCommentedExample([]byte(tt.line))
		if ok != tt.wantOK || key != tt.key || tip != tt.tip {
			t.Errorf("parseCommentedExample(%q) = %q, %q, %v; want %q, %q, %v", tt.line, key, tip, ok, tt.key, tt.tip, tt.wantOK)
		}
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
