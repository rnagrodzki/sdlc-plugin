package tools

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/rnagrodzki/sdlc-plugin/internal/execx"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
)

// fakeToolCatalog is a toolCatalog for tests. tools maps each tool name to
// its action enum; an empty list is a tool with no action enum.
type fakeToolCatalog struct {
	tools map[string][]string
}

// ToolNames returns the tool names of the fake catalog, sorted.
func (f *fakeToolCatalog) ToolNames() []string {
	names := make([]string, 0, len(f.tools))
	for n := range f.tools {
		names = append(names, n)
	}
	slices.Sort(names)
	return names
}

// ToolActions returns the action enum of the named tool and true, or an
// empty slice and false for a name that the fake catalog does not hold.
func (f *fakeToolCatalog) ToolActions(name string) ([]string, bool) {
	actions, ok := f.tools[name]
	return append([]string{}, actions...), ok
}

// deadRefTestCatalog returns a fake catalog with three tools: execute_state
// and ship_state with an action enum, and prepare_orchestrator without one.
func deadRefTestCatalog() *fakeToolCatalog {
	return &fakeToolCatalog{tools: map[string][]string{
		"execute_state":        {"wave-start", "wave-done", "task-context"},
		"ship_state":           {"read", "defer"},
		"prepare_orchestrator": {},
	}}
}

// deadRefTrackedFiles is the tracked-file list that stubGitLsFiles gives in
// most tests: a small copy of this repository's layout.
var deadRefTrackedFiles = []string{
	"internal/mcpserver/render.go",
	"internal/mcpserver/errors.go",
	"internal/tools/harden.go",
	"plugins/sdlc/skills/setup/SKILL.md",
	"plugins/sdlc/skills/setup/setup-dimensions.md",
	"plugins/sdlc/hooks/hooks.json",
	".sdlc-v2/config.toml",
}

// stubGitLsFiles replaces hardenGitLsFiles with a fake that returns files and
// err, and restores the real one when the test ends.
func stubGitLsFiles(t *testing.T, files []string, err error) {
	t.Helper()
	prev := hardenGitLsFiles
	hardenGitLsFiles = func(string) ([]string, error) { return files, err }
	t.Cleanup(func() { hardenGitLsFiles = prev })
}

// deadRefShipSkill is a SKILL.md for the skill "ship" with three flags in its
// argument-hint (--auto, --quick, --bump) and Step headings at levels two and
// three.
const deadRefShipSkill = "---\n" +
	"name: ship\n" +
	"argument-hint: \"[--auto] [--quick] [--bump patch|minor]\"\n" +
	"---\n\n" +
	"# Ship\n\n" +
	"## Step 0.5: Read state\n\n" +
	"## Step 1: Parse arguments\n\n" +
	"### Step 6b — Review\n\n" +
	"### Step 9 — Cleanup\n"

// newDeadRefPlugin writes a plugin tree with one SKILL.md for each entry of
// skills (name to file text) and points CLAUDE_PLUGIN_ROOT at it.
func newDeadRefPlugin(t *testing.T, skills map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, content := range skills {
		writeFile(t, filepath.Join(root, "skills", name, "SKILL.md"), content)
	}
	if len(skills) == 0 {
		if err := os.MkdirAll(filepath.Join(root, "skills"), 0o755); err != nil {
			t.Fatalf("mkdir skills: %v", err)
		}
	}
	t.Setenv("CLAUDE_PLUGIN_ROOT", root)
	return root
}

// scanGuardrailText runs scanDeadReferences on one plan guardrail whose
// description is text, with the test catalog.
func scanGuardrailText(t *testing.T, text string) ([]deadReference, int, []string) {
	t.Helper()
	s := hardenSurfaces{PlanGuardrails: []surfaceGuardrail{{ID: "rule", Description: text}}}
	return scanDeadReferences(t.TempDir(), s, deadRefTestCatalog())
}

// deadRefKindTokens returns "kind:token" for each reference, in order.
func deadRefKindTokens(refs []deadReference) []string {
	out := make([]string, 0, len(refs))
	for _, r := range refs {
		out = append(out, r.Kind+":"+r.Token)
	}
	return out
}

// TestHardenDeadRefs_RealCases scans a review-dimension file that holds the
// six real dead references of the rule set: three stale paths, two removed
// tools and a step that the skill no longer has. Each one is reported with
// its kind, its line and the dimension name, and nothing else is reported.
func TestHardenDeadRefs_RealCases(t *testing.T) {
	stubGitLsFiles(t, deadRefTrackedFiles, nil)
	newDeadRefPlugin(t, map[string]string{"ship": deadRefShipSkill})

	dir := t.TempDir()
	var b strings.Builder
	b.WriteString("---\nname: mcp-contract-compliance\nseverity: high\n---\n")
	for i := 5; i < 93; i++ {
		b.WriteString("filler line\n")
	}
	b.WriteString("error is a defect. Both live in `internal/mcpserver/envelope.go` (`defaultRecovery`, called from `renderError`).\n") // line 93
	b.WriteString("- Secrets never go in `.sdlc-v2/config.json`.\n")                                                                    // line 94
	b.WriteString("- Local overrides live in `.sdlc-v2/local.json`, never committed.\n")                                                // line 95
	b.WriteString("- The skill calls `harden_prepare({failureText})` before the agent.\n")                                              // line 96
	b.WriteString("- The skill calls `mcp__plugin_sdlc_sdlc__error_report_prepare` for tooling errors.\n")                              // line 97
	b.WriteString("- Stop the workers in `/sdlc:ship Step 12` before the PR.\n")                                                        // line 98
	file := filepath.Join(dir, "mcp-contract-compliance.md")
	writeFile(t, file, b.String())

	refs, truncated, skips := scanDeadReferences(dir, hardenSurfaces{
		ReviewDimensions: []reviewDimensionMeta{{Name: "mcp-contract-compliance", Path: file}},
	}, deadRefTestCatalog())

	want := []deadReference{
		{Surface: "review-dimensions", RuleID: "mcp-contract-compliance", File: file, Line: 93, Kind: "path", Token: "internal/mcpserver/envelope.go", Reason: "no tracked file internal/mcpserver/envelope.go"},
		{Surface: "review-dimensions", RuleID: "mcp-contract-compliance", File: file, Line: 94, Kind: "path", Token: ".sdlc-v2/config.json", Reason: "no tracked file .sdlc-v2/config.json"},
		{Surface: "review-dimensions", RuleID: "mcp-contract-compliance", File: file, Line: 95, Kind: "path", Token: ".sdlc-v2/local.json", Reason: "no tracked file .sdlc-v2/local.json"},
		{Surface: "review-dimensions", RuleID: "mcp-contract-compliance", File: file, Line: 96, Kind: "tool", Token: "harden_prepare", Reason: "no MCP tool named harden_prepare"},
		{Surface: "review-dimensions", RuleID: "mcp-contract-compliance", File: file, Line: 97, Kind: "tool", Token: "error_report_prepare", Reason: "no MCP tool named error_report_prepare"},
		{Surface: "review-dimensions", RuleID: "mcp-contract-compliance", File: file, Line: 98, Kind: "step", Token: "/sdlc:ship Step 12", Reason: "skill ship has no Step 12 heading"},
	}
	if !slices.Equal(refs, want) {
		t.Errorf("refs:\n got %+v\nwant %+v", refs, want)
	}
	if truncated != 0 || len(skips) != 0 {
		t.Errorf("truncated = %d, skips = %v; want 0 and none", truncated, skips)
	}
}

// TestHardenDeadRefs_DeadTokenPerKind checks one dead token of each kind and
// form in a guardrail description. Each gives exactly the listed hit, and a
// guardrail hit has no line number.
func TestHardenDeadRefs_DeadTokenPerKind(t *testing.T) {
	stubGitLsFiles(t, deadRefTrackedFiles, nil)
	newDeadRefPlugin(t, map[string]string{"ship": deadRefShipSkill})

	cases := []struct {
		name string
		text string
		want []string
	}{
		{"path with line suffix", "See `internal/tools/gone.go:12-14`.", []string{"path:internal/tools/gone.go:12-14"}},
		{"path with dot slash", "See `./docs/gone.md`.", []string{"path:./docs/gone.md"}},
		{"glob", "Match `docs/**/*.rst`.", []string{"path:docs/**/*.rst"}},
		{"tool call", "Call `gone_tool()`.", []string{"tool:gone_tool"}},
		{"tool prefix", "Call `mcp__plugin_sdlc_sdlc__gone`.", []string{"tool:gone"}},
		{"action", "Call `execute_state({ action: \"wave-begin\" })`.", []string{"action:wave-begin"}},
		{"action single quotes", "Call `ship_state({'action':'drop'})`.", []string{"action:drop"}},
		{"flag after /sdlc:skill", "Run `/sdlc:ship --turbo`.", []string{"flag:--turbo"}},
		{"flag after bare skill with value", "Run `ship --auto --turbo=1`.", []string{"flag:--turbo"}},
		{"step after bare skill", "Read `ship Step 3`.", []string{"step:ship Step 3"}},
		{"same token twice on one line", "See `gone_tool()` and `gone_tool()`.", []string{"tool:gone_tool"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			refs, _, skips := scanGuardrailText(t, tc.text)
			if got := deadRefKindTokens(refs); !slices.Equal(got, tc.want) {
				t.Errorf("hits = %v, want %v", got, tc.want)
			}
			for _, r := range refs {
				if r.Line != 0 || r.Surface != "plan-guardrails" || r.RuleID != "rule" {
					t.Errorf("guardrail hit must carry no line and the rule id: %+v", r)
				}
			}
			if len(skips) != 0 {
				t.Errorf("skips = %v, want none", skips)
			}
		})
	}
}

// TestHardenDeadRefs_LiveTokensReportNothing checks tokens that exist: a
// tracked path (exact, by suffix, with a line suffix), a matching glob, a
// known tool and action, a tool with no action enum, a flag in the
// argument-hint and Step headings at each level and form. None is reported.
func TestHardenDeadRefs_LiveTokensReportNothing(t *testing.T) {
	stubGitLsFiles(t, deadRefTrackedFiles, nil)
	newDeadRefPlugin(t, map[string]string{"ship": deadRefShipSkill})

	for _, text := range []string{
		"See `internal/tools/harden.go`.",
		"See `skills/setup/SKILL.md` and `hooks/hooks.json`.",
		"See `internal/tools/harden.go:785-790`.",
		"Match `internal/**/*.go` and `*.toml`.",
		"Call `execute_state({action:\"wave-start\"})`.",
		"Call `mcp__plugin_sdlc_sdlc__ship_state`.",
		"Call `prepare_orchestrator({action:\"anything\"})`.",
		"Run `/sdlc:ship --auto --bump minor`.",
		"Read `ship Step 1`, `ship Step 6b`, `ship Step 9` and `ship Step 0.5`.",
	} {
		refs, _, _ := scanGuardrailText(t, text)
		if len(refs) != 0 {
			t.Errorf("%q: refs = %+v, want none", text, refs)
		}
	}
}

// TestHardenDeadRefs_ExclusionsReportNothing checks each skip rule of the
// token table: placeholders, URLs, example lines, spans with spaces, text
// that is not a path (including a file name with no "/"), bare snake_case,
// calls that are not tool calls, flags and steps with no known skill, and text
// outside backticks. None of them is reported.
func TestHardenDeadRefs_ExclusionsReportNothing(t *testing.T) {
	stubGitLsFiles(t, deadRefTrackedFiles, nil)
	newDeadRefPlugin(t, map[string]string{"ship": deadRefShipSkill})

	cases := map[string]string{
		"placeholder <":         "See `<dir>/SKILL.md`.",
		"placeholder {":         "See `{root}/gone.go`.",
		"placeholder $":         "See `$HOME/gone.go`.",
		"placeholder ~":         "See `~/gone.go`.",
		"url":                   "See `https://gone.org/gone.md`.",
		"www":                   "See `www.gone.org/a.md`.",
		"Example line":          "Example: `internal/gone.go`.",
		"lower example line":    "For example `internal/gone.go`.",
		"e.g. line":             "A path, e.g. `internal/gone.go`.",
		"span with a space":     "Run `go test ./internal/gone/...`.",
		"no extension":          "See `internal/gone`.",
		"directory":             "See `internal/gone/`.",
		"dot file name only":    "See `docs/.github`.",
		"file name with no /":   "See `gone.go`.",
		"glob with no / or ext": "Ask `ok?`.",
		"symbol glob":           "Find each `mcpserver.Register*` call.",
		"glob that won't build": "Match `src/[z-a].go`.",
		"bare snake_case":       "The `harden_prepare` tool.",
		"call with no _":        "Call `main()` and `now()`.",
		"method call":           "Call `obj.do_thing()`.",
		"flag with no skill":    "Pass `--turbo`.",
		"skill not first word":  "Run `git ship --turbo`.",
		"unknown /sdlc: skill":  "Run `/sdlc:gone --turbo`.",
		"step with no skill":    "Read `Step 12`.",
		"step with other word":  "Read `in Step 12`.",
		"step of unknown skill": "Read `gone Step 12`.",
		"outside backticks":     "Call harden_prepare() in internal/gone.go.",
	}
	for name, text := range cases {
		refs, _, _ := scanGuardrailText(t, text)
		if len(refs) != 0 {
			t.Errorf("%s: %q: refs = %+v, want none", name, text, refs)
		}
	}
}

// TestHardenDeadRefs_ActionOfUnknownToolIsReportedAsTool checks that a call
// that names an action of a tool not in the catalog gives one tool hit and no
// action hit.
func TestHardenDeadRefs_ActionOfUnknownToolIsReportedAsTool(t *testing.T) {
	stubGitLsFiles(t, deadRefTrackedFiles, nil)
	newDeadRefPlugin(t, map[string]string{"ship": deadRefShipSkill})

	refs, _, _ := scanGuardrailText(t, "Call `gone_tool({action:\"x\"})`.")
	if got := deadRefKindTokens(refs); !slices.Equal(got, []string{"tool:gone_tool"}) {
		t.Errorf("hits = %v, want [tool:gone_tool]", got)
	}
}

// TestHardenDeadRefs_SurfaceOrderAndIdentity scans one rule of each surface.
// The hits come in the order plan, execute, dimension, Copilot; a dimension
// with no name uses its file stem; only markdown hits carry a line.
func TestHardenDeadRefs_SurfaceOrderAndIdentity(t *testing.T) {
	stubGitLsFiles(t, deadRefTrackedFiles, nil)
	newDeadRefPlugin(t, map[string]string{"ship": deadRefShipSkill})

	root := t.TempDir()
	dimFile := filepath.Join(root, "dims", "no-name.md")
	writeFile(t, dimFile, "---\nseverity: low\n---\nCall `dim_gone()`.\n")
	copFile := filepath.Join(root, "inst", "go.instructions.md")
	writeFile(t, copFile, "line one\nline two\nCall `cop_gone()`.\n")

	refs, _, skips := scanDeadReferences(root, hardenSurfaces{
		PlanGuardrails:      []surfaceGuardrail{{ID: "p", Description: "Call `plan_gone()`."}},
		ExecuteGuardrails:   []surfaceGuardrail{{ID: "e", Description: "Call `exec_gone()`."}},
		ReviewDimensions:    []reviewDimensionMeta{{Path: dimFile}},
		CopilotInstructions: []copilotInstruction{{Name: "go", Path: copFile}},
	}, deadRefTestCatalog())
	if len(skips) != 0 {
		t.Errorf("skips = %v, want none", skips)
	}

	configFile := filepath.Join(root, paths.DataDir, paths.ConfigFile)
	want := []deadReference{
		{Surface: "plan-guardrails", RuleID: "p", File: configFile, Kind: "tool", Token: "plan_gone", Reason: "no MCP tool named plan_gone"},
		{Surface: "execute-guardrails", RuleID: "e", File: configFile, Kind: "tool", Token: "exec_gone", Reason: "no MCP tool named exec_gone"},
		{Surface: "review-dimensions", RuleID: "no-name", File: dimFile, Line: 4, Kind: "tool", Token: "dim_gone", Reason: "no MCP tool named dim_gone"},
		{Surface: "copilot-instructions", RuleID: "go", File: copFile, Line: 3, Kind: "tool", Token: "cop_gone", Reason: "no MCP tool named cop_gone"},
	}
	if !slices.Equal(refs, want) {
		t.Errorf("refs:\n got %+v\nwant %+v", refs, want)
	}
}

// TestHardenDeadRefs_GitLsFilesFails checks that a failed git ls-files adds
// the path skip note and turns the path check off, while the tool check still
// runs.
func TestHardenDeadRefs_GitLsFilesFails(t *testing.T) {
	stubGitLsFiles(t, nil, errors.New("not a git repository"))
	newDeadRefPlugin(t, map[string]string{"ship": deadRefShipSkill})

	refs, _, skips := scanGuardrailText(t, "See `internal/gone.go` and call `gone_tool()`.")
	if got := deadRefKindTokens(refs); !slices.Equal(got, []string{"tool:gone_tool"}) {
		t.Errorf("hits = %v, want [tool:gone_tool]", got)
	}
	want := []string{"path check skipped: git ls-files failed: not a git repository"}
	if !slices.Equal(skips, want) {
		t.Errorf("skips = %v, want %v", skips, want)
	}
}

// TestHardenDeadRefs_TooManyTrackedFiles checks that more than 100000 tracked
// files add the path skip note and report no path.
func TestHardenDeadRefs_TooManyTrackedFiles(t *testing.T) {
	stubGitLsFiles(t, make([]string, deadRefMaxTrackedFiles+1), nil)
	newDeadRefPlugin(t, map[string]string{"ship": deadRefShipSkill})

	refs, _, skips := scanGuardrailText(t, "See `internal/gone.go`.")
	if len(refs) != 0 {
		t.Errorf("refs = %+v, want none", refs)
	}
	want := []string{"path check skipped: more than 100000 tracked files"}
	if !slices.Equal(skips, want) {
		t.Errorf("skips = %v, want %v", skips, want)
	}
}

// TestHardenDeadRefs_GitLsFilesOutputCap checks that a git ls-files output
// over the execx cap (wrapped execx.ErrOutputCap) gives the too-many-files
// skip note, not the "git ls-files failed" note, and reports no path.
func TestHardenDeadRefs_GitLsFilesOutputCap(t *testing.T) {
	stubGitLsFiles(t, nil, fmt.Errorf("run git: %w", execx.ErrOutputCap))
	newDeadRefPlugin(t, map[string]string{"ship": deadRefShipSkill})

	refs, _, skips := scanGuardrailText(t, "See `internal/gone.go`.")
	if len(refs) != 0 {
		t.Errorf("refs = %+v, want none", refs)
	}
	want := []string{"path check skipped: more than 100000 tracked files (git ls-files output is over the output cap)"}
	if !slices.Equal(skips, want) {
		t.Errorf("skips = %v, want %v", skips, want)
	}
}

// TestHardenDeadRefs_NilCatalog checks that a nil catalog adds the tool skip
// note and reports no tool or action, while the path check still runs.
func TestHardenDeadRefs_NilCatalog(t *testing.T) {
	stubGitLsFiles(t, deadRefTrackedFiles, nil)
	newDeadRefPlugin(t, map[string]string{"ship": deadRefShipSkill})

	s := hardenSurfaces{PlanGuardrails: []surfaceGuardrail{{ID: "rule", Description: "Call `gone_tool({action:\"x\"})` in `internal/gone.go`."}}}
	refs, _, skips := scanDeadReferences(t.TempDir(), s, nil)
	if got := deadRefKindTokens(refs); !slices.Equal(got, []string{"path:internal/gone.go"}) {
		t.Errorf("hits = %v, want [path:internal/gone.go]", got)
	}
	want := []string{"tool and action checks skipped: no tool catalog"}
	if !slices.Equal(skips, want) {
		t.Errorf("skips = %v, want %v", skips, want)
	}
}

// TestHardenDeadRefs_RuleFileTooLargeOrUnreadable checks that a dimension file
// of 1 MiB plus 1 byte and a Copilot file that does not exist each add a
// "<file> skipped: <cause>" note and give no hit.
func TestHardenDeadRefs_RuleFileTooLargeOrUnreadable(t *testing.T) {
	stubGitLsFiles(t, deadRefTrackedFiles, nil)
	newDeadRefPlugin(t, map[string]string{"ship": deadRefShipSkill})

	root := t.TempDir()
	big := filepath.Join(root, "big.md")
	writeFile(t, big, "`gone_tool()`\n"+strings.Repeat("a", deadRefMaxFileBytes+1-len("`gone_tool()`\n")))
	missing := filepath.Join(root, "missing.instructions.md")

	refs, _, skips := scanDeadReferences(root, hardenSurfaces{
		ReviewDimensions:    []reviewDimensionMeta{{Name: "big", Path: big}},
		CopilotInstructions: []copilotInstruction{{Name: "missing", Path: missing}},
	}, deadRefTestCatalog())
	if len(refs) != 0 {
		t.Errorf("refs = %+v, want none", refs)
	}
	if len(skips) != 2 || skips[0] != big+" skipped: larger than 1 MiB" ||
		!strings.HasPrefix(skips[1], missing+" skipped: ") {
		t.Errorf("skips = %v, want the size note for %s and a read note for %s", skips, big, missing)
	}
}

// TestHardenDeadRefs_SkillFileTooLargeOrUnreadable checks that a SKILL.md of
// 1 MiB plus 1 byte and a skill directory with no SKILL.md each add one
// "<file> skipped: <cause>" note, even when the skill is cited twice, and give
// no flag or step hit.
func TestHardenDeadRefs_SkillFileTooLargeOrUnreadable(t *testing.T) {
	stubGitLsFiles(t, deadRefTrackedFiles, nil)
	root := newDeadRefPlugin(t, map[string]string{"ship": strings.Repeat("a", deadRefMaxFileBytes+1)})
	if err := os.MkdirAll(filepath.Join(root, "skills", "pr"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	refs, _, skips := scanGuardrailText(t, "Run `ship --turbo`, `ship Step 12`, `/sdlc:pr --turbo` and `pr Step 12`.")
	if len(refs) != 0 {
		t.Errorf("refs = %+v, want none", refs)
	}
	shipFile := filepath.Join(root, "skills", "ship", "SKILL.md")
	prFile := filepath.Join(root, "skills", "pr", "SKILL.md")
	if len(skips) != 2 || skips[0] != shipFile+" skipped: larger than 1 MiB" ||
		!strings.HasPrefix(skips[1], prFile+" skipped: ") {
		t.Errorf("skips = %v, want one note for %s and one for %s", skips, shipFile, prFile)
	}
}

// TestHardenDeadRefs_SkillFrontmatterDoesNotParse checks that a SKILL.md with
// an unclosed frontmatter adds the flag skip note for that skill, reports no
// flag, and still reports a dead step.
func TestHardenDeadRefs_SkillFrontmatterDoesNotParse(t *testing.T) {
	stubGitLsFiles(t, deadRefTrackedFiles, nil)
	newDeadRefPlugin(t, map[string]string{"ship": "---\nargument-hint: \"[--auto]\"\n## Step 1: Go\n"})

	refs, _, skips := scanGuardrailText(t, "Run `ship --turbo` then `ship Step 12`; `ship Step 1` is fine.")
	if got := deadRefKindTokens(refs); !slices.Equal(got, []string{"step:ship Step 12"}) {
		t.Errorf("hits = %v, want [step:ship Step 12]", got)
	}
	if len(skips) != 1 || !strings.HasPrefix(skips[0], "flag check skipped for ship: ") {
		t.Errorf("skips = %v, want one flag skip note for ship", skips)
	}
}

// TestHardenDeadRefs_NoSkillsDirectory checks that a plugin root with no
// skills directory adds the flag and step skip note and reports no flag or
// step.
func TestHardenDeadRefs_NoSkillsDirectory(t *testing.T) {
	stubGitLsFiles(t, deadRefTrackedFiles, nil)
	t.Setenv("CLAUDE_PLUGIN_ROOT", t.TempDir())

	refs, _, skips := scanGuardrailText(t, "Run `ship --turbo` then `ship Step 12`.")
	if len(refs) != 0 {
		t.Errorf("refs = %+v, want none", refs)
	}
	if len(skips) != 1 || !strings.HasPrefix(skips[0], "flag and step checks skipped: read ") {
		t.Errorf("skips = %v, want one read note for the skills directory", skips)
	}
}

// TestHardenDeadRefs_PluginRootNotFound checks that with no CLAUDE_PLUGIN_ROOT
// and no plugin.json above the test binary, the scan adds the plugin-root
// skip note and reports no flag or step.
func TestHardenDeadRefs_PluginRootNotFound(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Skipf("executable path unknown: %v", err)
	}
	if _, found := findPluginRootFrom(filepath.Dir(exe)); found {
		t.Skip("a plugin.json lies above the test binary")
	}
	stubGitLsFiles(t, deadRefTrackedFiles, nil)
	t.Setenv("CLAUDE_PLUGIN_ROOT", "")

	refs, _, skips := scanGuardrailText(t, "Run `ship --turbo` then `ship Step 12`.")
	if len(refs) != 0 {
		t.Errorf("refs = %+v, want none", refs)
	}
	if !slices.Equal(skips, []string{"flag and step checks skipped: plugin root not found"}) {
		t.Errorf("skips = %v, want the plugin-root note", skips)
	}
}

// TestHardenDeadRefs_TruncatesAfter200 checks that 205 dead paths give the
// first 200 in order and a truncated count of 5.
func TestHardenDeadRefs_TruncatesAfter200(t *testing.T) {
	stubGitLsFiles(t, deadRefTrackedFiles, nil)
	newDeadRefPlugin(t, map[string]string{"ship": deadRefShipSkill})

	var b strings.Builder
	for i := 0; i < 205; i++ {
		fmt.Fprintf(&b, "See `gone/file%d.go`.\n", i)
	}
	refs, truncated, _ := scanGuardrailText(t, b.String())
	if len(refs) != 200 || truncated != 5 {
		t.Fatalf("len(refs) = %d, truncated = %d; want 200 and 5", len(refs), truncated)
	}
	if refs[0].Token != "gone/file0.go" || refs[199].Token != "gone/file199.go" {
		t.Errorf("kept the wrong hits: first %q, last %q", refs[0].Token, refs[199].Token)
	}
}

// deadPathLines returns n lines, each citing one path that no tracked file
// matches: "See `gone/file<i>.go`." for i from 0 to n-1.
func deadPathLines(n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "See `gone/file%d.go`.\n", i)
	}
	return b.String()
}

// TestHardenDeadRefs_HitCapBoundary checks the edge of the 200-hit cap: 199 and
// exactly 200 dead references are all kept with a truncated count of 0, and
// 201 keep the first 200 with a truncated count of 1.
func TestHardenDeadRefs_HitCapBoundary(t *testing.T) {
	stubGitLsFiles(t, deadRefTrackedFiles, nil)
	newDeadRefPlugin(t, map[string]string{"ship": deadRefShipSkill})

	cases := []struct {
		name          string
		dead          int
		wantKept      int
		wantTruncated int
	}{
		{"one below the cap", 199, 199, 0},
		{"at the cap", 200, 200, 0},
		{"one above the cap", 201, 200, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			refs, truncated, _ := scanGuardrailText(t, deadPathLines(tc.dead))
			if len(refs) != tc.wantKept || truncated != tc.wantTruncated {
				t.Fatalf("len(refs) = %d, truncated = %d; want %d and %d", len(refs), truncated, tc.wantKept, tc.wantTruncated)
			}
			if want := fmt.Sprintf("gone/file%d.go", tc.wantKept-1); refs[len(refs)-1].Token != want {
				t.Errorf("last kept hit = %q, want %q", refs[len(refs)-1].Token, want)
			}
		})
	}
}

// TestHardenDeadRefs_TrackedFilesAtCapRunsPathCheck checks that exactly 100000
// tracked files still run the path check: the dead path is reported and the
// scan adds no skip note.
func TestHardenDeadRefs_TrackedFilesAtCapRunsPathCheck(t *testing.T) {
	stubGitLsFiles(t, make([]string, 100000), nil)
	newDeadRefPlugin(t, map[string]string{"ship": deadRefShipSkill})

	refs, _, skips := scanGuardrailText(t, "See `internal/gone.go`.")
	if got := deadRefKindTokens(refs); !slices.Equal(got, []string{"path:internal/gone.go"}) {
		t.Errorf("hits = %v, want [path:internal/gone.go]", got)
	}
	if len(skips) != 0 {
		t.Errorf("skips = %v, want none", skips)
	}
}

// TestHardenDeadRefs_RuleFileAtSizeLimitIsScanned checks that a dimension file
// and a Copilot file of exactly 1048576 bytes are read and scanned: each gives
// its tool hit and the scan adds no skip note.
func TestHardenDeadRefs_RuleFileAtSizeLimitIsScanned(t *testing.T) {
	stubGitLsFiles(t, deadRefTrackedFiles, nil)
	newDeadRefPlugin(t, map[string]string{"ship": deadRefShipSkill})

	root := t.TempDir()
	const head = "`gone_tool()`\n"
	content := head + strings.Repeat("a", 1048576-len(head))
	dimFile := filepath.Join(root, "dim.md")
	copFile := filepath.Join(root, "cop.instructions.md")
	writeFile(t, dimFile, content)
	writeFile(t, copFile, content)

	refs, _, skips := scanDeadReferences(root, hardenSurfaces{
		ReviewDimensions:    []reviewDimensionMeta{{Name: "dim", Path: dimFile}},
		CopilotInstructions: []copilotInstruction{{Name: "cop", Path: copFile}},
	}, deadRefTestCatalog())
	if got := deadRefKindTokens(refs); !slices.Equal(got, []string{"tool:gone_tool", "tool:gone_tool"}) {
		t.Errorf("hits = %v, want one tool hit for each file", got)
	}
	if len(skips) != 0 {
		t.Errorf("skips = %v, want none", skips)
	}
}

// TestHardenDeadRefs_SkillFileAtSizeLimitIsScanned checks that a SKILL.md of
// exactly 1048576 bytes is read and used: a dead flag and a dead step are
// reported and the scan adds no skip note.
func TestHardenDeadRefs_SkillFileAtSizeLimitIsScanned(t *testing.T) {
	stubGitLsFiles(t, deadRefTrackedFiles, nil)
	newDeadRefPlugin(t, map[string]string{"ship": deadRefShipSkill + strings.Repeat("a", 1048576-len(deadRefShipSkill))})

	refs, _, skips := scanGuardrailText(t, "Run `ship --turbo` and `ship Step 12`.")
	if got := deadRefKindTokens(refs); !slices.Equal(got, []string{"flag:--turbo", "step:ship Step 12"}) {
		t.Errorf("hits = %v, want [flag:--turbo step:ship Step 12]", got)
	}
	if len(skips) != 0 {
		t.Errorf("skips = %v, want none", skips)
	}
}

// TestHardenDeadRefs_RuleFileReadFails checks that a dimension file that opens
// but cannot be read (the path is a directory) adds one "<file> skipped: read
// <file>: <cause>" note, not the size note, and gives no hit.
func TestHardenDeadRefs_RuleFileReadFails(t *testing.T) {
	stubGitLsFiles(t, deadRefTrackedFiles, nil)
	newDeadRefPlugin(t, map[string]string{"ship": deadRefShipSkill})

	dirAsFile := t.TempDir()
	refs, _, skips := scanDeadReferences(t.TempDir(), hardenSurfaces{
		ReviewDimensions: []reviewDimensionMeta{{Name: "dir", Path: dirAsFile}},
	}, deadRefTestCatalog())
	if len(refs) != 0 {
		t.Errorf("refs = %+v, want none", refs)
	}
	if len(skips) != 1 || !strings.HasPrefix(skips[0], dirAsFile+" skipped: read "+dirAsFile+": ") ||
		strings.Contains(skips[0], "larger than 1 MiB") {
		t.Errorf("skips = %v, want one read-failure note for %s", skips, dirAsFile)
	}
}

// stubExecutable replaces deadRefExecutable with a fake that returns exe and
// err, and restores the real one when the test ends.
func stubExecutable(t *testing.T, exe string, err error) {
	t.Helper()
	prev := deadRefExecutable
	deadRefExecutable = func() (string, error) { return exe, err }
	t.Cleanup(func() { deadRefExecutable = prev })
}

// newDeadRefPluginTree writes a plugin tree with a .claude-plugin/plugin.json
// and a ship skill, and returns its root. It does not set CLAUDE_PLUGIN_ROOT,
// so the scan must find the tree by the walk up from the executable.
func newDeadRefPluginTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".claude-plugin", "plugin.json"), "{}")
	writeFile(t, filepath.Join(root, "skills", "ship", "SKILL.md"), deadRefShipSkill)
	t.Setenv("CLAUDE_PLUGIN_ROOT", "")
	return root
}

// TestHardenDeadRefs_PluginRootFoundFromExecutable checks the normal run of
// the installed plugin: with no CLAUDE_PLUGIN_ROOT, the walk up from the
// executable's directory finds the plugin root, so a dead flag is reported
// and the scan adds no skip note.
func TestHardenDeadRefs_PluginRootFoundFromExecutable(t *testing.T) {
	stubGitLsFiles(t, deadRefTrackedFiles, nil)
	root := newDeadRefPluginTree(t)
	stubExecutable(t, filepath.Join(root, "bin", "sdlc"), nil)

	refs, _, skips := scanGuardrailText(t, "Run `ship --turbo`.")
	if got := deadRefKindTokens(refs); !slices.Equal(got, []string{"flag:--turbo"}) {
		t.Errorf("hits = %v, want [flag:--turbo]", got)
	}
	if len(skips) != 0 {
		t.Errorf("skips = %v, want none", skips)
	}
}

// TestHardenDeadRefs_PluginRootFoundFromWorkingDirectory checks that when the
// executable path is unknown, the walk starts at the working directory and
// finds the plugin root there.
func TestHardenDeadRefs_PluginRootFoundFromWorkingDirectory(t *testing.T) {
	stubGitLsFiles(t, deadRefTrackedFiles, nil)
	root := newDeadRefPluginTree(t)
	stubExecutable(t, "", errors.New("executable path unknown"))
	t.Chdir(root)

	refs, _, skips := scanGuardrailText(t, "Run `ship --turbo`.")
	if got := deadRefKindTokens(refs); !slices.Equal(got, []string{"flag:--turbo"}) {
		t.Errorf("hits = %v, want [flag:--turbo]", got)
	}
	if len(skips) != 0 {
		t.Errorf("skips = %v, want none", skips)
	}
}

// TestLoadDeadRefSkillNames_ListsOnlyDirectories checks that a plain file in
// the skills directory is not a skill name: only the sub-directories are.
func TestLoadDeadRefSkillNames_ListsOnlyDirectories(t *testing.T) {
	root := newDeadRefPlugin(t, map[string]string{"ship": deadRefShipSkill, "pr": deadRefShipSkill})
	writeFile(t, filepath.Join(root, "skills", "NOTES"), "not a skill")

	dir, known, note := loadDeadRefSkillNames()
	if note != "" {
		t.Fatalf("note = %q, want none", note)
	}
	if want := filepath.Join(root, "skills"); dir != want {
		t.Errorf("dir = %q, want %q", dir, want)
	}
	if want := map[string]bool{"ship": true, "pr": true}; !maps.Equal(known, want) {
		t.Errorf("known = %v, want %v", known, want)
	}
}

// TestHardenDeadRefs_SkillWithoutArgumentHintSkipsFlagCheck checks that a
// SKILL.md whose frontmatter has no argument-hint adds one flag skip note and
// reports no flag for that skill, still reports a dead step for it, and that a
// skill with an argument-hint keeps its flag check.
func TestHardenDeadRefs_SkillWithoutArgumentHintSkipsFlagCheck(t *testing.T) {
	stubGitLsFiles(t, deadRefTrackedFiles, nil)
	newDeadRefPlugin(t, map[string]string{
		"error-report": "---\nname: error-report\n---\n\n## Step 1: Report\n",
		"ship":         deadRefShipSkill,
	})

	refs, _, skips := scanGuardrailText(t, ""+
		"Run `/sdlc:error-report --verbose` and `error-report --quiet`, read `error-report Step 1` "+
		"and `error-report Step 9`, then run `/sdlc:ship --turbo`.")
	if got := deadRefKindTokens(refs); !slices.Equal(got, []string{"step:error-report Step 9", "flag:--turbo"}) {
		t.Errorf("hits = %v, want [step:error-report Step 9 flag:--turbo]", got)
	}
	if want := []string{"flag check skipped for error-report: no argument-hint"}; !slices.Equal(skips, want) {
		t.Errorf("skips = %v, want %v", skips, want)
	}
}

// TestHardenPrepare_DeadReferencesCleanRuleSet runs hardenPrepare on a clean
// rule set with a catalog, a fake git ls-files and a plugin root. The
// manifest holds deadReferences and deadReferenceSkips as empty arrays, not
// null, and no deadReferencesTruncated key.
func TestHardenPrepare_DeadReferencesCleanRuleSet(t *testing.T) {
	redirectTempDir(t)
	stubGitLsFiles(t, deadRefTrackedFiles, nil)
	newDeadRefPlugin(t, map[string]string{"ship": deadRefShipSkill})
	root := t.TempDir()
	writeFile(t, filepath.Join(root, paths.DataDir, "config.toml"), ""+
		"[plan.guardrails.good-rule]\n"+
		"description = \"Every task names `internal/tools/harden.go` and calls `execute_state({action:\\\"wave-start\\\"})`.\"\n")

	out, err := hardenPrepare(root, root, HardenPrepareIn{
		FailureText:     "boom",
		Skill:           "ship",
		SkipConfigCheck: true,
		catalog:         deadRefTestCatalog(),
	})
	if err != nil {
		t.Fatalf("hardenPrepare: %v", err)
	}
	raw, err := os.ReadFile(out.ManifestPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var m struct {
		Surfaces map[string]json.RawMessage `json:"surfaces"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal manifest: %v", err)
	}
	for _, key := range []string{"deadReferences", "deadReferenceSkips"} {
		if got := string(m.Surfaces[key]); got != "[]" {
			t.Errorf("surfaces.%s = %s, want []", key, got)
		}
	}
	if _, ok := m.Surfaces["deadReferencesTruncated"]; ok {
		t.Errorf("surfaces.deadReferencesTruncated must be absent when nothing is cut")
	}
}

// TestHardenPrepare_DeadReferencesInManifest runs hardenPrepare on a rule that
// cites a removed tool. With a catalog the manifest lists the tool hit; with
// no catalog it lists no hit and the tool skip note, and the errors key keeps
// no entry for the scan.
func TestHardenPrepare_DeadReferencesInManifest(t *testing.T) {
	redirectTempDir(t)
	stubGitLsFiles(t, deadRefTrackedFiles, nil)
	newDeadRefPlugin(t, map[string]string{"ship": deadRefShipSkill})
	root := t.TempDir()
	writeFile(t, filepath.Join(root, paths.DataDir, "config.toml"), ""+
		"[execute.guardrails.old-tool]\n"+
		"description = \"Call `harden_prepare()` first.\"\n")

	run := func(cat toolCatalog) map[string]any {
		out, err := hardenPrepare(root, root, HardenPrepareIn{FailureText: "boom", Skill: "ship", SkipConfigCheck: true, catalog: cat})
		if err != nil {
			t.Fatalf("hardenPrepare: %v", err)
		}
		return readHardenManifest(t, out.ManifestPath)
	}

	surfaces := run(deadRefTestCatalog())["surfaces"].(map[string]any)
	refs := surfaces["deadReferences"].([]any)
	if len(refs) != 1 {
		t.Fatalf("deadReferences = %v, want 1 entry", refs)
	}
	ref := refs[0].(map[string]any)
	if ref["surface"] != "execute-guardrails" || ref["ruleId"] != "old-tool" || ref["kind"] != "tool" || ref["token"] != "harden_prepare" {
		t.Errorf("unexpected entry: %v", ref)
	}
	if _, ok := ref["line"]; ok {
		t.Errorf("a guardrail entry must have no line: %v", ref)
	}

	manifest := run(nil)
	surfaces = manifest["surfaces"].(map[string]any)
	if refs := surfaces["deadReferences"].([]any); len(refs) != 0 {
		t.Errorf("deadReferences = %v, want none with no catalog", refs)
	}
	skips := surfaces["deadReferenceSkips"].([]any)
	if len(skips) != 1 || skips[0] != "tool and action checks skipped: no tool catalog" {
		t.Errorf("deadReferenceSkips = %v, want the catalog note", skips)
	}
	for _, e := range manifest["errors"].([]any) {
		if strings.Contains(fmt.Sprint(e), "skipped") {
			t.Errorf("a skip note must not go to errors: %v", e)
		}
	}
}
