package mcpserver

import (
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/rnagrodzki/sdlc-plugin/internal/pipeline"
)

// Local fixtures. Package mcpserver can never import internal/tools (tools
// imports mcpserver), so these mirror the real output shapes instead of using
// them: a flat struct, a depth-2 struct carrying a nested "next" like
// TemplateResolution, a wide struct like PRPrepareOut, a slice of structs like
// the plan lanes, and a map[string]any like execute_state. pipeline.Narration
// is the one real type used here -- internal/pipeline does not import
// mcpserver.

type fxFlat struct {
	Name    string  `json:"name"`
	Count   int     `json:"count"`
	Enabled bool    `json:"enabled"`
	Ratio   float64 `json:"ratio"`
}

type fxRouting struct {
	PipelineMode string `json:"pipelineMode"`
	FileCount    int    `json:"fileCount"`
}

type fxTemplateResolution struct {
	Next           string    `json:"next"`
	HeaderMarkdown string    `json:"headerMarkdown"`
	Routing        fxRouting `json:"routing"`
}

// fxNested mirrors PlanPrepareOut: a "next" at depth 2 under template, and no
// "next" at the root.
type fxNested struct {
	ActiveTemplatePath string               `json:"activeTemplatePath"`
	Template           fxTemplateResolution `json:"template"`
}

type fxLane struct {
	Name     string `json:"name"`
	Severity string `json:"severity"`
}

type fxLanes struct {
	Next     string   `json:"next"`
	Summary  string   `json:"summary"`
	Warnings []string `json:"warnings"`
	Tags     []string `json:"tags"`
	Lanes    []fxLane `json:"lanes"`
}

type fxEmpties struct {
	Name   string            `json:"name"`
	Tags   []string          `json:"tags"`
	Empty  []string          `json:"empty"`
	Lanes  []fxLane          `json:"lanes"`
	Config map[string]string `json:"config"`
}

type fxBlob struct {
	Diff string `json:"diff"`
}

type fxMigration struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// fxPointers mirrors CommitPrepareOut's pointer fields.
type fxPointers struct {
	Migration         *fxMigration `json:"migration"`
	LastCommitMessage *string      `json:"lastCommitMessage"`
	Attempts          *int         `json:"attempts"`
}

type fxOmit struct {
	Always     string   `json:"always"`
	MaybeText  string   `json:"maybeText,omitempty"`
	MaybeCount int      `json:"maybeCount,omitempty"`
	MaybeList  []string `json:"maybeList,omitempty"`
	MaybeFlag  bool     `json:"maybeFlag,omitempty"`
}

type fxNode struct {
	Name string  `json:"name"`
	Self *fxNode `json:"self,omitempty"`
}

// fxWide mirrors PRPrepareOut's width (34 fields).
type fxWide struct {
	F01 string   `json:"f01"`
	F02 string   `json:"f02"`
	F03 string   `json:"f03"`
	F04 string   `json:"f04"`
	F05 string   `json:"f05"`
	F06 string   `json:"f06"`
	F07 string   `json:"f07"`
	F08 string   `json:"f08"`
	F09 string   `json:"f09"`
	F10 string   `json:"f10"`
	F11 int      `json:"f11"`
	F12 int      `json:"f12"`
	F13 int      `json:"f13"`
	F14 int      `json:"f14"`
	F15 bool     `json:"f15"`
	F16 bool     `json:"f16"`
	F17 bool     `json:"f17"`
	F18 bool     `json:"f18"`
	F19 []string `json:"f19"`
	F20 []string `json:"f20"`
	F21 string   `json:"f21"`
	F22 string   `json:"f22"`
	F23 string   `json:"f23"`
	F24 string   `json:"f24"`
	F25 string   `json:"f25"`
	F26 string   `json:"f26"`
	F27 string   `json:"f27"`
	F28 string   `json:"f28"`
	F29 string   `json:"f29"`
	F30 string   `json:"f30"`
	F31 float64  `json:"f31"`
	F32 float64  `json:"f32"`
	F33 *string  `json:"f33"`
	F34 *int     `json:"f34"`
}

func wideFixture() fxWide {
	text := "value"
	number := 7
	return fxWide{
		F01: "alpha", F02: "beta", F03: "gamma", F04: "delta", F05: "epsilon",
		F06: "zeta", F07: "eta", F08: "theta", F09: "iota", F10: "kappa",
		F11: 1, F12: 2, F13: 3, F14: 4,
		F15: true, F16: false, F17: true, F18: false,
		F19: []string{"one", "two"}, F20: nil,
		F21: "lambda", F22: "mu", F23: "nu", F24: "xi", F25: "omicron",
		F26: "pi", F27: "rho", F28: "sigma", F29: "tau", F30: "upsilon",
		F31: 1.5, F32: 0,
		F33: &text, F34: &number,
	}
}

// --- Rule 1 / title line ---

func TestRenderOKTitleLine(t *testing.T) {
	out := renderOK("validate", fxFlat{Name: "config", Count: 2, Enabled: true, Ratio: 0.5})

	first := strings.SplitN(out, "\n", 2)[0]
	titleRE := regexp.MustCompile(`^# (?P<tool>[a-z_]+) — ok$`)
	if !titleRE.MatchString(first) {
		t.Fatalf("first line %q does not match the contract regexp", first)
	}
	if first != "# validate — ok" {
		t.Fatalf("first line = %q, want %q", first, "# validate — ok")
	}
	for _, want := range []string{"- name: config", "- count: 2", "- enabled: true", "- ratio: 0.5"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestRenderOKRootNextHoisted(t *testing.T) {
	out := renderOK("plan_prepare", fxLanes{
		Next:    "call plan_mark with marker=\"skillInvoked\"",
		Summary: "Template resolved.",
	})

	if !strings.Contains(out, "**Next:** call plan_mark with marker=\"skillInvoked\"\n") {
		t.Fatalf("missing hoisted Next line:\n%s", out)
	}
	if strings.Contains(out, "- next:") {
		t.Fatalf("hoisted next must not also appear as a field bullet:\n%s", out)
	}
	if !strings.Contains(out, "## Summary\nTemplate resolved.\n") {
		t.Fatalf("missing Summary section:\n%s", out)
	}
}

func TestRenderOKMapRootNextHoisted(t *testing.T) {
	out := renderOK("execute_state", map[string]any{
		"next":  "call execute_state with action=\"wave-start\"",
		"runId": "20260918T190632",
	})

	if !strings.Contains(out, "**Next:** call execute_state with action=\"wave-start\"\n") {
		t.Fatalf("map root did not hoist next:\n%s", out)
	}
	if strings.Contains(out, "- next:") {
		t.Fatalf("hoisted next must not appear under Fields:\n%s", out)
	}
	if !strings.Contains(out, "- runId: 20260918T190632") {
		t.Fatalf("map root lost a field:\n%s", out)
	}
}

func TestRenderOKNestedNextNotHoisted(t *testing.T) {
	out := renderOK("plan_prepare", fxNested{
		ActiveTemplatePath: "/tmp/plan-template-default.md",
		Template: fxTemplateResolution{
			Next:           "fill the template",
			HeaderMarkdown: "# My Plan",
			Routing:        fxRouting{PipelineMode: "full", FileCount: 12},
		},
	})

	if strings.Contains(out, "**Next:**") {
		t.Fatalf("a depth-2 next must not be hoisted:\n%s", out)
	}
	templateAt := strings.Index(out, "\n## template\n")
	nextAt := strings.Index(out, "\n- next: fill the template\n")
	if templateAt < 0 || nextAt < 0 || nextAt < templateAt {
		t.Fatalf("nested next did not render in place under ## template:\n%s", out)
	}
	if !strings.Contains(out, "\n### template.routing\n") {
		t.Fatalf("missing dotted depth-2 heading:\n%s", out)
	}
	if !strings.Contains(out, "- pipelineMode: full") || !strings.Contains(out, "- fileCount: 12") {
		t.Fatalf("depth-2 section lost fields:\n%s", out)
	}
}

// --- Rules 3, 6, 7 ---

func TestRenderOKWarningsSection(t *testing.T) {
	out := renderOK("plan_prepare", fxLanes{
		Warnings: []string{"config.toml uses a legacy key: plan.reviewers", "second warning"},
	})

	want := "## Warnings\n- config.toml uses a legacy key: plan.reviewers\n- second warning\n"
	if !strings.Contains(out, want) {
		t.Fatalf("missing warnings bullet list:\n%s", out)
	}
}

func TestRenderOKScalarSliceBullets(t *testing.T) {
	out := renderOK("plan_prepare", fxLanes{Tags: []string{"alpha", "beta"}})

	if !strings.Contains(out, "- tags:\n  - alpha\n  - beta\n") {
		t.Fatalf("scalar slice did not render as an indented bullet list:\n%s", out)
	}
}

func TestRenderOKSliceOfStructsIndexed(t *testing.T) {
	out := renderOK("plan_prepare", fxLanes{Lanes: []fxLane{
		{Name: "file-existence", Severity: "error"},
		{Name: "static-structural", Severity: "warning"},
	}})

	for _, want := range []string{
		"## lanes[0]\n- name: file-existence\n- severity: error\n",
		"## lanes[1]\n- name: static-structural\n- severity: warning\n",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing indexed section %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "\n## lanes\n") {
		t.Fatalf("a non-empty slice of structs must not emit a bare parent heading:\n%s", out)
	}
}

// --- Rule 8 ---

func TestRenderOKNilAndEmptyCollectionsRenderNone(t *testing.T) {
	nilOut := renderOK("links_validate", fxEmpties{Name: "x"})
	emptyOut := renderOK("links_validate", fxEmpties{
		Name:   "x",
		Tags:   []string{},
		Empty:  []string{},
		Lanes:  []fxLane{},
		Config: map[string]string{},
	})

	if nilOut != emptyOut {
		t.Fatalf("nil and len==0 collections must render identically:\nnil:\n%s\nempty:\n%s", nilOut, emptyOut)
	}
	for _, want := range []string{
		"- tags: (none)",
		"- empty: (none)",
		"## lanes\n(none)\n",
		"## config\n(none)\n",
	} {
		if !strings.Contains(nilOut, want) {
			t.Fatalf("missing %q:\n%s", want, nilOut)
		}
	}
	if strings.Contains(nilOut, "null") {
		t.Fatalf("output must never contain JSON null:\n%s", nilOut)
	}
	if strings.Contains(nilOut, "lanes[0]") {
		t.Fatalf("an empty section must not emit index headings:\n%s", nilOut)
	}
}

// --- Rules 9 and 10 ---

func TestRenderOKFencesBacktickContent(t *testing.T) {
	blob := "before\n```go\nfmt.Println(\"x\")\n```\nafter"
	out := renderOK("commit_prepare", fxBlob{Diff: blob})

	want := "- diff:\n````\n" + blob + "\n````\n"
	if !strings.Contains(out, want) {
		t.Fatalf("multiline blob not wrapped in a closing 4-backtick fence:\n%s", out)
	}
	if got := strings.Count(out, "\n````\n"); got != 2 {
		t.Fatalf("expected exactly one open and one close fence, got %d:\n%s", got, out)
	}
}

func TestRenderOKRawDisplayIsVerbatim(t *testing.T) {
	narration := pipeline.Narration{
		Summary: "Wave 1 complete",
		Display: "## Wave 1 complete\n\n| task | status |\n|---|---|\n| 1 | done |",
		Next:    &pipeline.NextAction{ID: "wave-2", Instruction: "start wave 2"},
	}

	out := renderOK("execute_state", narration)

	if !strings.Contains(out, "- display:\n"+narration.Display+"\n") {
		t.Fatalf("Display must render verbatim under its key:\n%s", out)
	}
	if strings.Contains(out, "```") {
		t.Fatalf("a render:\"raw\" field must never be fenced:\n%s", out)
	}
	if !strings.Contains(out, "## Summary\nWave 1 complete\n") {
		t.Fatalf("missing Summary section:\n%s", out)
	}
	// Narration.Next is a *NextAction, not a string: it keeps its own section.
	if strings.Contains(out, "**Next:**") {
		t.Fatalf("an object next must not be hoisted:\n%s", out)
	}
	if !strings.Contains(out, "## next\n- id: wave-2\n- instruction: start wave 2\n") {
		t.Fatalf("pointer-to-struct next did not render as a section:\n%s", out)
	}
}

// --- Rule 11 ---

func TestRenderOKMapKeysAreSorted(t *testing.T) {
	for i := range 100 {
		out := renderOK("jira", map[string]any{"z": 1, "a": 2, "m": 3})
		a, m, z := strings.Index(out, "- a: 2"), strings.Index(out, "- m: 3"), strings.Index(out, "- z: 1")
		if a < 0 || m < 0 || z < 0 {
			t.Fatalf("iteration %d lost a key:\n%s", i, out)
		}
		if !(a < m && m < z) {
			t.Fatalf("iteration %d rendered keys out of order (a=%d m=%d z=%d):\n%s", i, a, m, z, out)
		}
	}
}

// --- Rule 12 ---

func TestRenderOKSelfReferentialPointerTerminates(t *testing.T) {
	node := &fxNode{Name: "root"}
	node.Self = node

	out := renderOK("validate", node)

	if !strings.Contains(out, "- name: root") {
		t.Fatalf("lost the root field:\n%s", out)
	}
	if !strings.Contains(out, "## self\n(cycle)\n") {
		t.Fatalf("expected the cycle to stop at the repeated pointer:\n%s", out)
	}
}

func TestRenderOKDepthCapFlattens(t *testing.T) {
	out := renderOK("execute_state", deepMap(8))

	if !strings.Contains(out, "\n###### l1.l2.l3.l4.l5.l6\n") {
		t.Fatalf("missing the deepest heading:\n%s", out)
	}
	if strings.Contains(out, "\n####### ") {
		t.Fatalf("heading level must be capped at 6:\n%s", out)
	}
	if !strings.Contains(out, "- l7.l8: bottom\n") {
		t.Fatalf("subtree past the depth cap did not flatten to a dotted path:\n%s", out)
	}
}

func deepMap(levels int) map[string]any {
	var inner any = "bottom"
	for i := levels; i >= 1; i-- {
		inner = map[string]any{fmt.Sprintf("l%d", i): inner}
	}
	nested, _ := inner.(map[string]any)
	return nested
}

// --- Rules 13 and 14 ---

func TestRenderOKPointerFields(t *testing.T) {
	message := "feat: add renderer"
	out := renderOK("commit_prepare", fxPointers{LastCommitMessage: &message})

	if !strings.Contains(out, "- lastCommitMessage: feat: add renderer") {
		t.Fatalf("non-nil *string did not render its pointee:\n%s", out)
	}
	if !strings.Contains(out, "- migration: (none)") || !strings.Contains(out, "- attempts: (none)") {
		t.Fatalf("nil pointers must render (none):\n%s", out)
	}
	if strings.Contains(out, "0x") {
		t.Fatalf("output must never contain a pointer address:\n%s", out)
	}

	withStruct := renderOK("commit_prepare", fxPointers{
		Migration:         &fxMigration{From: "0.1.4", To: "0.1.5"},
		LastCommitMessage: &message,
	})
	if !strings.Contains(withStruct, "## migration\n- from: 0.1.4\n- to: 0.1.5\n") {
		t.Fatalf("non-nil *struct did not render as a dereferenced section:\n%s", withStruct)
	}
}

func TestRenderOKOmitemptyMatchesJSON(t *testing.T) {
	cases := []struct {
		name  string
		value fxOmit
	}{
		{"zero", fxOmit{Always: "kept"}},
		{"populated", fxOmit{
			Always:     "kept",
			MaybeText:  "present",
			MaybeCount: 3,
			MaybeList:  []string{"a"},
			MaybeFlag:  true,
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := renderOK("plan_support", tc.value)
			rendered := renderedKeys(out)
			emitted := jsonKeys(t, tc.value)

			for name := range emitted {
				if !rendered[name] {
					t.Errorf("json emits %q but the renderer dropped it:\n%s", name, out)
				}
			}
			for _, name := range structJSONNames(reflect.TypeFor[fxOmit]()) {
				if emitted[name] {
					continue
				}
				if rendered[name] {
					t.Errorf("json omits %q but the renderer printed it:\n%s", name, out)
				}
			}
		})
	}
}

// --- Rule 15 ---

func TestRenderOKInterfaceElementsUnwrapped(t *testing.T) {
	out := renderOK("execute_state", map[string]any{
		"scalar": "plain",
		"object": map[string]any{"depth": 2},
		"items":  []any{map[string]any{"name": "first"}, "loose"},
		"lane":   fxLane{Name: "file-existence", Severity: "error"},
	})

	for _, want := range []string{
		"- scalar: plain",
		"## object\n- depth: 2\n",
		"## items[0]\n- name: first\n",
		"## items[1]\nloose\n",
		"## lane\n- name: file-existence\n- severity: error\n",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q:\n%s", want, out)
		}
	}
}

// --- Field completeness ---

// TestRenderOKFieldCompleteness is the property that catches a lossy walk:
// every key encoding/json emits for a fixture must appear in the rendered
// output, as a bullet key, a section heading, or the hoisted Next line.
func TestRenderOKFieldCompleteness(t *testing.T) {
	message := "feat: add renderer"
	fixtures := []struct {
		name  string
		value any
	}{
		{"flat", fxFlat{Name: "config", Count: 2, Enabled: true, Ratio: 0.5}},
		{"nested", fxNested{
			ActiveTemplatePath: "/tmp/plan-template-default.md",
			Template: fxTemplateResolution{
				Next:           "fill the template",
				HeaderMarkdown: "# My Plan\n\nbody",
				Routing:        fxRouting{PipelineMode: "full", FileCount: 12},
			},
		}},
		{"lanes", fxLanes{
			Next:     "call plan_mark",
			Summary:  "Template resolved.",
			Warnings: []string{"legacy key"},
			Tags:     []string{"a", "b"},
			Lanes:    []fxLane{{Name: "file-existence", Severity: "error"}},
		}},
		{"empties", fxEmpties{Name: "x"}},
		{"pointers", fxPointers{Migration: &fxMigration{From: "0.1.4", To: "0.1.5"}, LastCommitMessage: &message}},
		{"wide", wideFixture()},
		{"blob", fxBlob{Diff: "before\n```go\ncode\n```\nafter"}},
		{"narration", pipeline.Narration{
			Summary: "Wave 1 complete",
			Display: "## Wave 1 complete",
			Timing:  &pipeline.TimingInfo{StepSeconds: 12, Human: "12s"},
			Next:    &pipeline.NextAction{ID: "wave-2", Instruction: "start wave 2"},
		}},
		{"map-root", map[string]any{
			"next":  "call execute_state",
			"runId": "20260918T190632",
			"waves": []any{map[string]any{"number": 1, "status": "in_progress"}},
		}},
		{"deep-map", deepMap(8)},
	}

	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			out := renderOK("tool_under_test", fixture.value)
			rendered := renderedKeys(out)
			for name := range jsonKeys(t, fixture.value) {
				if !rendered[name] {
					t.Errorf("key %q is emitted by encoding/json but missing from the render:\n%s", name, out)
				}
			}
		})
	}
}

// renderedKeys collects the keys a rendered result names: bullet keys, section
// headings (dotted paths split into segments, index suffixes stripped), and the
// hoisted Next line. Only the three structural headings are case-folded --
// every other key is compared exactly, so a renderer that mangled key case
// would fail.
func renderedKeys(out string) map[string]bool {
	keys := map[string]bool{}
	add := func(path string) {
		for _, segment := range strings.Split(path, ".") {
			if i := strings.Index(segment, "["); i >= 0 {
				segment = segment[:i]
			}
			if segment = strings.TrimSpace(segment); segment != "" {
				keys[segment] = true
			}
		}
	}

	for _, line := range strings.Split(out, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "**Next:**"):
			keys["next"] = true
		case strings.HasPrefix(trimmed, "#"):
			text := strings.TrimSpace(strings.TrimLeft(trimmed, "#"))
			switch text {
			case "Fields": // structural: names no key
			case "Summary":
				keys["summary"] = true
			case "Warnings":
				keys["warnings"] = true
			default:
				add(text)
			}
		case strings.HasPrefix(trimmed, "- "):
			rest := trimmed[len("- "):]
			if i := strings.Index(rest, ":"); i > 0 {
				add(rest[:i])
			}
		}
	}
	return keys
}

// jsonKeys returns every key encoding/json emits for value, at any depth.
func jsonKeys(t *testing.T, value any) map[string]bool {
	t.Helper()

	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}

	keys := map[string]bool{}
	var walk func(any)
	walk = func(node any) {
		switch typed := node.(type) {
		case map[string]any:
			for key, child := range typed {
				keys[key] = true
				walk(child)
			}
		case []any:
			for _, child := range typed {
				walk(child)
			}
		}
	}
	walk(decoded)
	return keys
}

// structJSONNames lists the JSON names of every exported field of t, including
// the ones omitempty may drop.
func structJSONNames(t reflect.Type) []string {
	names := make([]string, 0, t.NumField())
	for i := range t.NumField() {
		field := t.Field(i)
		if !field.IsExported() {
			continue
		}
		name := field.Name
		if tag, ok := field.Tag.Lookup("json"); ok {
			if tagged, _, _ := strings.Cut(tag, ","); tagged == "-" {
				continue
			} else if tagged != "" {
				name = tagged
			}
		}
		names = append(names, name)
	}
	return names
}
