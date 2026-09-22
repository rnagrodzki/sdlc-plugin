// Package skillcheck lints the "--flag" tokens that one skill instructs
// another skill to receive against the flag list the receiving skill
// documents. Issue #51 existed because received-review told harden to run
// with --auto while harden documented no such flag, and nothing compared the
// two texts.
//
// MATCHING RULE. It is deliberately narrow: a lint that fires on unrelated
// prose gets disabled instead of fixed, so every rule below must name the
// receiving skill. A flag is DISPATCHED only when it sits in the argument
// text of one of these dispatch instructions:
//
//  1. Skill("<skill>", "<args>")                   the second string argument
//  2. `Skill(<skill>)` with `<a>`, `<b>`           the run of inline code spans after "with"
//  3. Agent → <skill>, ..., args `<a>`             ship's dispatch notation
//  4. dispatch <skill> with `<a>`, `<b>`           the same run of spans, or
//     dispatch <skill> (`<a>`, `<b>`)              a parenthesised run
//  5. `/<skill> <args>` or `/sdlc:<skill> <args>`  a slash-command code span
//
// Everything else is prose, never a dispatch. That covers a caller's own flag
// mentions ("Suppressed when `--auto` is set", "propagating `--auto` to each
// dispatch", "(never `--branch`)", "no args (or `--base <branch>`)"). A run of
// code spans ends at the first thing that is not another code span: a blank
// line, a list bullet, a full stop or a word. Text inside double quotes is a
// value, not a flag ("--failure-text \"... --auto ...\""). A skill that
// dispatches to itself is documenting its own usage and is skipped here.
//
// A skill DOCUMENTS a flag when it appears in the skill's `argument-hint`
// frontmatter, or as a whole table cell of the SKILL.md body that is one
// inline code span starting with the flag (| `--auto` | ... |). The
// description and free prose do not count: a flag mentioned only there is
// not a documented flag. If the flag-table format changes, change
// flagParityDocumented with it.
//
// This file also holds the helpers shared by the harden, received-review and
// deferred parity files. It contains no production logic. Every unexported
// identifier is prefixed flagParity.
package skillcheck

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// flagParityFlagRe finds a "--flag" token. The leading group stops a longer
// dash run ("---") and a word glued to dashes ("a--b") from starting a flag.
// Submatch 1 is the flag.
var flagParityFlagRe = regexp.MustCompile(`(?:^|[^A-Za-z0-9_-])(--[a-z][a-z0-9]*(?:-[a-z0-9]+)*)`)

// flagParityFlagCellRe matches a table cell that is exactly one inline code
// span starting with a flag, such as `--resolve <id>`.
var flagParityFlagCellRe = regexp.MustCompile("^`(--[a-z][a-z0-9]*(?:-[a-z0-9]+)*)[^`]*`$")

// The five dispatch shapes of the matching rule, in order.
var (
	flagParitySkillCallRe  = regexp.MustCompile(`\bSkill\(\s*"(?:sdlc:)?([a-z][a-z0-9-]*)"\s*,\s*"`)
	flagParitySkillWithRe  = regexp.MustCompile("\\bSkill\\((?:sdlc:)?([a-z][a-z0-9-]*)\\)`?\\s+with\\s+")
	flagParityAgentArgsRe  = regexp.MustCompile("Agent\\s*(?:→|->)\\s*([a-z][a-z0-9-]*)[^`\\n]{0,80}?\\bargs\\s+`([^`]*)`")
	flagParityProseWithRe  = regexp.MustCompile(`\bdispatch(?:es|ing)?\s+([a-z][a-z0-9-]*)\s+with\s+`)
	flagParityProseParenRe = regexp.MustCompile(`\bdispatch(?:es|ing)?\s+([a-z][a-z0-9-]*)\s*\(`)
	flagParitySlashRe      = regexp.MustCompile("`/(?:sdlc:)?([a-z][a-z0-9-]*)(\\s[^`]*)`")
)

// flagParityDispatch is one flag a skill hands to another skill.
type flagParityDispatch struct {
	from string // skill whose SKILL.md holds the instruction
	to   string // skill that receives the flag
	flag string // for example "--auto"
	line int    // 1-based line of the flag token in from/SKILL.md
}

// flagParityBlankQuoted returns s with every double-quoted value, and the
// quote characters themselves, replaced by spaces. It keeps the length, so
// offsets into the result are offsets into s. Both a plain quote and an
// escaped quote (\") open and close a value.
func flagParityBlankQuoted(s string) string {
	b := []byte(s)
	inQuote := false
	for i := 0; i < len(b); i++ {
		switch {
		case b[i] == '\\' && i+1 < len(b) && b[i+1] == '"':
			inQuote = !inQuote
			b[i], b[i+1] = ' ', ' '
			i++
		case b[i] == '"':
			inQuote = !inQuote
			b[i] = ' '
		case inQuote:
			b[i] = ' '
		}
	}
	return string(b)
}

// flagParityStringEnd returns the index of the unescaped double quote that
// closes a string whose content starts at start, or -1 when it never closes.
func flagParityStringEnd(content string, start int) int {
	for i := start; i < len(content); i++ {
		switch content[i] {
		case '\\':
			i++
		case '"':
			return i
		}
	}
	return -1
}

// flagParitySpanChain returns the [start, end) content offsets of the run of
// inline code spans that begins at pos. Between two spans it allows spaces,
// commas and one line break. It stops at a blank line, and at the first
// character that does not open another span.
func flagParitySpanChain(content string, pos int) [][2]int {
	var spans [][2]int
	i := pos
	for {
		j, newlines := i, 0
	skip:
		for j < len(content) {
			switch content[j] {
			case ' ', '\t', '\r', ',':
				j++
			case '\n':
				newlines++
				if newlines > 1 {
					return spans
				}
				j++
			default:
				break skip
			}
		}
		if j >= len(content) || content[j] != '`' {
			return spans
		}
		end := strings.IndexByte(content[j+1:], '`')
		if end < 0 {
			return spans
		}
		spans = append(spans, [2]int{j + 1, j + 1 + end})
		i = j + 1 + end + 1
	}
}

// flagParitySkillCall is a Skill("<to>", "<args>") call. The raw argument
// string is content[start:end].
type flagParitySkillCall struct {
	to         string
	start, end int
}

func flagParitySkillCalls(content string) []flagParitySkillCall {
	var calls []flagParitySkillCall
	for _, m := range flagParitySkillCallRe.FindAllStringSubmatchIndex(content, -1) {
		end := flagParityStringEnd(content, m[1])
		if end < 0 {
			continue
		}
		calls = append(calls, flagParitySkillCall{to: content[m[2]:m[3]], start: m[1], end: end})
	}
	return calls
}

// flagParityExtract applies the matching rule to one SKILL.md and returns
// every flag it dispatches, in line order. Self-dispatches are returned too;
// the caller decides what to do with them. skills is the set of real skill
// names: shapes 3 to 5 ignore any other target, because prose such as
// "dispatch agents with `mode: ...`" is not a skill dispatch. Shapes 1 and 2
// call the Skill tool by name, so they keep every target and let the caller
// report an unknown one.
func flagParityExtract(from, content string, skills map[string]bool) []flagParityDispatch {
	var out []flagParityDispatch
	emit := func(to string, start int, raw string) {
		blanked := flagParityBlankQuoted(raw)
		for _, m := range flagParityFlagRe.FindAllStringSubmatchIndex(blanked, -1) {
			out = append(out, flagParityDispatch{
				from: from,
				to:   to,
				flag: blanked[m[2]:m[3]],
				line: 1 + strings.Count(content[:start+m[2]], "\n"),
			})
		}
	}
	chain := func(to string, pos int) {
		for _, sp := range flagParitySpanChain(content, pos) {
			emit(to, sp[0], content[sp[0]:sp[1]])
		}
	}

	for _, c := range flagParitySkillCalls(content) {
		emit(c.to, c.start, content[c.start:c.end])
	}
	for _, m := range flagParitySkillWithRe.FindAllStringSubmatchIndex(content, -1) {
		chain(content[m[2]:m[3]], m[1])
	}
	for _, m := range flagParityAgentArgsRe.FindAllStringSubmatchIndex(content, -1) {
		if to := content[m[2]:m[3]]; skills[to] {
			emit(to, m[4], content[m[4]:m[5]])
		}
	}
	for _, re := range []*regexp.Regexp{flagParityProseWithRe, flagParityProseParenRe} {
		for _, m := range re.FindAllStringSubmatchIndex(content, -1) {
			if to := content[m[2]:m[3]]; skills[to] {
				chain(to, m[1])
			}
		}
	}
	for _, m := range flagParitySlashRe.FindAllStringSubmatchIndex(content, -1) {
		if to := content[m[2]:m[3]]; skills[to] {
			emit(to, m[4], content[m[4]:m[5]])
		}
	}

	sort.SliceStable(out, func(i, j int) bool { return out[i].line < out[j].line })
	return out
}

// flagParityFrontmatterField returns the unquoted value of a one-line
// frontmatter key, or "" when the file has no such key.
func flagParityFrontmatterField(content, key string) string {
	lines := strings.Split(content, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return ""
	}
	for _, line := range lines[1:] {
		if strings.TrimSpace(line) == "---" {
			break
		}
		if rest, ok := strings.CutPrefix(line, key+":"); ok {
			return strings.Trim(strings.TrimSpace(rest), `"`)
		}
	}
	return ""
}

// flagParityBody returns the file content after the frontmatter block.
func flagParityBody(content string) string {
	lines := strings.SplitAfter(content, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return content
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			return strings.Join(lines[i+1:], "")
		}
	}
	return content
}

// flagParityFlagSet returns every flag token found in text.
func flagParityFlagSet(text string) map[string]bool {
	set := map[string]bool{}
	for _, m := range flagParityFlagRe.FindAllStringSubmatch(text, -1) {
		set[m[1]] = true
	}
	return set
}

// flagParityHintFlags returns the flags in the argument-hint frontmatter.
func flagParityHintFlags(content string) map[string]bool {
	return flagParityFlagSet(flagParityFrontmatterField(content, "argument-hint"))
}

// flagParitySplitCells splits a markdown table row into cells. A "|" inside an
// inline code span (`--quality full|balanced`) does not split.
func flagParitySplitCells(line string) []string {
	var cells []string
	var cur strings.Builder
	inCode := false
	for i := 0; i < len(line); i++ {
		switch c := line[i]; {
		case c == '`':
			inCode = !inCode
			cur.WriteByte(c)
		case c == '\\' && i+1 < len(line) && line[i+1] == '|':
			cur.WriteByte('|')
			i++
		case c == '|' && !inCode:
			cells = append(cells, cur.String())
			cur.Reset()
		default:
			cur.WriteByte(c)
		}
	}
	return append(cells, cur.String())
}

// flagParityTableFlags returns the flags declared as whole table cells: a cell
// that is one inline code span starting with a flag. A flag mentioned inside a
// longer description cell is not declared by it.
func flagParityTableFlags(content string) map[string]bool {
	set := map[string]bool{}
	for _, line := range strings.Split(content, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "|") {
			continue
		}
		for _, cell := range flagParitySplitCells(line) {
			if m := flagParityFlagCellRe.FindStringSubmatch(strings.TrimSpace(cell)); m != nil {
				set[m[1]] = true
			}
		}
	}
	return set
}

// flagParityDocumented returns the flags a SKILL.md documents: its
// argument-hint flags plus its table-cell flags.
func flagParityDocumented(content string) map[string]bool {
	set := flagParityHintFlags(content)
	for f := range flagParityTableFlags(content) {
		set[f] = true
	}
	return set
}

// flagParityProblems checks every dispatch found in contents (skill name to
// SKILL.md text). It returns one message per undocumented flag, and every
// non-self dispatch it found, so callers can pin that the matcher still sees
// the real dispatches.
func flagParityProblems(contents map[string]string) ([]string, []flagParityDispatch) {
	known := make(map[string]bool, len(contents))
	documented := make(map[string]map[string]bool, len(contents))
	names := make([]string, 0, len(contents))
	for name, c := range contents {
		known[name] = true
		documented[name] = flagParityDocumented(c)
		names = append(names, name)
	}
	sort.Strings(names)

	var problems []string
	var found []flagParityDispatch
	seen := map[string]bool{}
	for _, from := range names {
		for _, d := range flagParityExtract(from, contents[from], known) {
			if d.to == from {
				continue
			}
			found = append(found, d)
			var msg string
			switch {
			case !known[d.to]:
				msg = fmt.Sprintf("%s/SKILL.md:%d dispatches %s to unknown skill %q, which has no skills/%s/SKILL.md",
					d.from, d.line, d.flag, d.to, d.to)
			case !documented[d.to][d.flag]:
				msg = fmt.Sprintf("%s/SKILL.md:%d dispatches %s to skill %q, but %q does not document that flag",
					d.from, d.line, d.flag, d.to, d.to)
			default:
				continue
			}
			if !seen[msg] {
				seen[msg] = true
				problems = append(problems, msg)
			}
		}
	}
	return problems, found
}

// flagParityLoadSkills reads every plugins/sdlc/skills/*/SKILL.md, keyed by
// the skill's directory name.
func flagParityLoadSkills(t *testing.T) map[string]string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(executeSkillsRepoRoot(t), "skills", "*", "SKILL.md"))
	if err != nil {
		t.Fatalf("glob skills/*/SKILL.md: %v", err)
	}
	if len(matches) == 0 {
		t.Fatal("flagParityLoadSkills: glob matched zero SKILL.md files")
	}
	contents := make(map[string]string, len(matches))
	for _, path := range matches {
		contents[filepath.Base(filepath.Dir(path))] = executeSkillsReadFile(t, path)
	}
	return contents
}

// flagParityTicks lets self-test fixtures use "~" for a backtick, so they can
// stay in raw string literals.
func flagParityTicks(s string) string { return strings.ReplaceAll(s, "~", "`") }

// flagParityKeys renders dispatches as sorted, unique "to:flag:line" strings.
func flagParityKeys(ds []flagParityDispatch) []string {
	set := map[string]bool{}
	for _, d := range ds {
		set[fmt.Sprintf("%s:%s:%d", d.to, d.flag, d.line)] = true
	}
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func TestSkillFlagParity(t *testing.T) {
	// Each case pairs a fixture with the exact set of flags the matching rule
	// must report, or must NOT report. The fixtures hold both dispatch shapes
	// and the prose traps that once looked like dispatches.
	known := map[string]bool{
		"harden": true, "pr": true, "commit": true, "review": true, "setup": true,
		"execute": true, "verify-pipeline": true, "received-review": true,
	}
	matcherCases := []struct {
		name    string
		content string
		want    []string
	}{
		{
			// Shape 1. The flags are inside one quoted argument. Quoted
			// values are skipped. A flag in a bracketed optional part counts.
			name: "skill_call_string",
			content: `intro
Skill("harden",
  "--failure-text \"cluster --quoted text\"
   --skill received-review
   [--auto when --auto was passed]"
)
`,
			want: []string{"harden:--auto:5", "harden:--failure-text:3", "harden:--skill:4"},
		},
		{
			// Shape 2. The run ends at the full stop. The caller's own
			// "--auto" mentions before it, after it and in a later bullet
			// are not dispatched.
			name: "skill_prose_chain",
			content: flagParityTicks(`Suppressed when ~--auto~ is set. When selected, dispatch ~Skill(harden)~ with
~--failure-text "reject --quoted"~,
~--skill commit~, ~--step "Step 5"~,
~--operation "op"~. Implements R16, propagating ~--auto~ to each dispatch.

- **cancel** - abort; ~--auto~ is ignored here
`),
			want: []string{"harden:--failure-text:2", "harden:--operation:4", "harden:--skill:3", "harden:--step:3"},
		},
		{
			// Shape 2 with no run: a word after "with", or a blank line.
			name: "skill_prose_no_chain",
			content: flagParityTicks(`dispatches ~Skill(harden)~ with the same flag shape as ~--auto~.
dispatch ~Skill(harden)~ with ~--skill commit~

~--auto~ is a caller flag.
`),
			want: []string{"harden:--skill:2"},
		},
		{
			// Shape 3. Only the span after "args" counts. Text after the
			// span, and "no args (or `--base`)", do not.
			name: "agent_args",
			content: flagParityTicks(`Dispatch: Agent → pr, model sonnet, args ~[--draft] [--base <branch>] --skip-approval [--auto]~ (never ~--label~)
Dispatch: Agent → execute, model sonnet, args ~{flags.args} <plan>~ (never ~--branch~, never ~--auto~)
Dispatch: Agent → review, model sonnet, no args (or ~--base <branch>~), never ~--committed~.
Poll: Agent → verify-pipeline dispatch (model sonnet, args ~--pr <N> --auto~) when it failed
`),
			want: []string{"pr:--auto:1", "pr:--base:1", "pr:--draft:1", "pr:--skip-approval:1", "verify-pipeline:--auto:4", "verify-pipeline:--pr:4"},
		},
		{
			// Shape 4, both forms. A prose "dispatch" with no "with" or run,
			// and a target that is not a skill, are ignored.
			name: "prose_dispatch",
			content: flagParityTicks(`- ~failed~ → dispatch verify-pipeline with ~--logs "<x --y>" --auto~, read its verdict
- ~actionable~ → dispatch received-review (~opus~, ~--pr~, ~--auto~=~flags.auto~); a fix
- dispatch harden for this cluster; propagating ~--auto~ to each dispatch.
- dispatch it with ~--zzz~
`),
			want: []string{"received-review:--auto:2", "received-review:--pr:2", "verify-pipeline:--auto:1", "verify-pipeline:--logs:1"},
		},
		{
			// Shape 5. A bare command, and a path that is not a skill, carry
			// no dispatch. Quoted values are skipped.
			name: "slash_spans",
			content: flagParityTicks(`- [~/setup --dimensions~](../setup/SKILL.md) creates review dimensions
- run ~/sdlc:harden --from-learnings~ to triage; ~/commit~ takes no flags; ~/tmp --nothing~ is a path
- ~/harden --failure-text "use --auto here" --skill plan~
`),
			want: []string{"harden:--failure-text:3", "harden:--from-learnings:2", "harden:--skill:3", "setup:--dimensions:1"},
		},
	}
	t.Run("matcher", func(t *testing.T) {
		for _, tc := range matcherCases {
			t.Run(tc.name, func(t *testing.T) {
				got := flagParityKeys(flagParityExtract("caller", tc.content, known))
				if strings.Join(got, " ") != strings.Join(tc.want, " ") {
					t.Errorf("dispatched flags mismatch\n got: %v\nwant: %v", got, tc.want)
				}
			})
		}
	})

	t.Run("documented_flags", func(t *testing.T) {
		content := flagParityTicks(`---
name: x
description: "Use --from-description. Arguments: --desc-only"
argument-hint: "[--a <x>] [--b | --c]"
---
| Mode | Flag | Required when |
|---|---|---|
| Inline | ~--table-flag <string>~ | Alternative to ~--prose-in-cell~ |
| ~--first-col~ | Description mentions ~--mention~ | |
| Pipe | ~--quality full|balanced~ | |
Optional: ~--prose-only~, ~--other~.
`)
		want := map[string]bool{"--a": true, "--b": true, "--c": true, "--table-flag": true, "--first-col": true, "--quality": true}
		flagParityAssertSameSet(t, "documented flags", flagParityDocumented(content), want)
	})

	t.Run("reports_undocumented_flag", func(t *testing.T) {
		caller := "x\nSkill(\"callee\", \"--known --zap\")\n"
		want := `caller/SKILL.md:2 dispatches --zap to skill "callee", but "callee" does not document that flag`
		// A flag that only the description or a prose line mentions is not
		// documented, so all three targets below leave --zap undocumented.
		targets := map[string]string{
			"hint_only":   "---\nargument-hint: \"[--known]\"\n---\n",
			"description": "---\ndescription: \"Use --zap when\"\nargument-hint: \"[--known]\"\n---\n",
			"prose":       "---\nargument-hint: \"[--known]\"\n---\nOptional: `--zap`.\n",
		}
		for name, callee := range targets {
			problems, _ := flagParityProblems(map[string]string{"caller": caller, "callee": callee})
			if len(problems) != 1 || problems[0] != want {
				t.Errorf("%s: problems = %q, want [%q]", name, problems, want)
			}
		}

		documented := "---\nargument-hint: \"[--known]\"\n---\n| Flag |\n|---|\n| `--zap` | zap |\n"
		if problems, _ := flagParityProblems(map[string]string{"caller": caller, "callee": documented}); len(problems) != 0 {
			t.Errorf("table-documented flag was reported: %q", problems)
		}

		selfDispatch := "---\nargument-hint: \"[--known]\"\n---\nSkill(\"callee\", \"--zap\")\n"
		if problems, _ := flagParityProblems(map[string]string{"callee": selfDispatch}); len(problems) != 0 {
			t.Errorf("self-dispatch was reported: %q", problems)
		}

		ghost := "x\nSkill(\"ghost\", \"--zap\")\n"
		problems, _ := flagParityProblems(map[string]string{"caller": ghost})
		if len(problems) != 1 || !strings.Contains(problems[0], `to unknown skill "ghost"`) {
			t.Errorf("unknown skill target: problems = %q", problems)
		}
	})

	// The real tree. Zero problems is required; a failure is either a skill
	// that dispatches a flag its target does not document, or a matcher that
	// stopped seeing the real dispatches.
	t.Run("plugin_skills", func(t *testing.T) {
		problems, found := flagParityProblems(flagParityLoadSkills(t))
		for _, p := range problems {
			t.Error(p)
		}

		if len(found) == 0 {
			t.Fatal("found zero dispatched flags across plugins/sdlc/skills/*/SKILL.md, so the matching rule no longer sees any dispatch shape used by the skills")
		}
		// One pin per shape the real tree relies on, so a matcher that goes
		// blind fails here instead of passing with nothing to check.
		pins := []struct{ from, to, flag, shape string }{
			{"received-review", "harden", "--auto", `Skill("harden", "<args>") call`},
			{"ship", "pr", "--skip-approval", "Agent → <skill>, args `<span>`"},
			{"commit", "harden", "--failure-text", "`Skill(harden)` with `<span>` run"},
		}
		for _, pin := range pins {
			ok := false
			for _, d := range found {
				ok = ok || (d.from == pin.from && d.to == pin.to && d.flag == pin.flag)
			}
			if !ok {
				t.Errorf("matcher no longer finds %s dispatching %s to %s (shape: %s)", pin.from, pin.flag, pin.to, pin.shape)
			}
		}
	})
}

// flagParityAssertSameSet fails when got and want hold different flags.
func flagParityAssertSameSet(t *testing.T, label string, got, want map[string]bool) {
	t.Helper()
	var missing, unexpected []string
	for f := range want {
		if !got[f] {
			missing = append(missing, f)
		}
	}
	for f := range got {
		if !want[f] {
			unexpected = append(unexpected, f)
		}
	}
	if len(missing)+len(unexpected) > 0 {
		sort.Strings(missing)
		sort.Strings(unexpected)
		t.Errorf("%s: flag lists disagree: missing %v, unexpected %v", label, missing, unexpected)
	}
}

// flagParityAssertSubset fails when got holds a flag that allowed does not.
func flagParityAssertSubset(t *testing.T, label string, got, allowed map[string]bool) {
	t.Helper()
	var unexpected []string
	for f := range got {
		if !allowed[f] {
			unexpected = append(unexpected, f)
		}
	}
	if len(unexpected) > 0 {
		sort.Strings(unexpected)
		t.Errorf("%s: flags %v are not declared by the skill", label, unexpected)
	}
}

// flagParityRegistry is the live MCP tool surface: the registered tool names,
// and for every tool whose input schema has an "action" enum, that enum.
type flagParityRegistry struct {
	tools   map[string]bool
	actions map[string]map[string]bool
}

// flagParityBuildRegistry lists the tools of a real in-process MCP server. It
// reuses executeSkillsListTools, which registers every Register*Tools family.
func flagParityBuildRegistry(t *testing.T) flagParityRegistry {
	t.Helper()
	reg := flagParityRegistry{tools: map[string]bool{}, actions: map[string]map[string]bool{}}
	for _, tl := range executeSkillsListTools(t) {
		reg.tools[tl.Name] = true
		raw, err := json.Marshal(tl.InputSchema)
		if err != nil {
			t.Fatalf("%s: marshal input schema: %v", tl.Name, err)
		}
		var schema struct {
			Properties struct {
				Action struct {
					Enum []string `json:"enum"`
				} `json:"action"`
			} `json:"properties"`
		}
		if err := json.Unmarshal(raw, &schema); err != nil {
			t.Fatalf("%s: unmarshal input schema: %v", tl.Name, err)
		}
		if len(schema.Properties.Action.Enum) == 0 {
			continue
		}
		set := make(map[string]bool, len(schema.Properties.Action.Enum))
		for _, v := range schema.Properties.Action.Enum {
			set[v] = true
		}
		reg.actions[tl.Name] = set
	}
	return reg
}

// flagParityToolCall is one name({ ... }) call in a skill file. action is the
// value of the call's top-level "action" key, or "" when it has none.
type flagParityToolCall struct {
	name   string
	action string
	line   int
}

// flagParityToolCalls mirrors planSkillsExtractCalls (same call regex, same
// built-in exclusions) and also reads each call's action value.
func flagParityToolCalls(content string) []flagParityToolCall {
	var calls []flagParityToolCall
	for _, m := range planSkillsCallRe.FindAllStringSubmatchIndex(content, -1) {
		name := content[m[2]:m[3]]
		if planSkillsBuiltinTools[name] || strings.HasPrefix(name, "mcp__") {
			continue
		}
		action, _ := flagParityCallAction(content, m[1]-1)
		calls = append(calls, flagParityToolCall{name: name, action: action, line: 1 + strings.Count(content[:m[0]], "\n")})
	}
	return calls
}

// flagParityCallAction reads the string value of the top-level "action" key
// of the object literal that opens at openBrace. Keys of nested objects and
// text inside string values are skipped, as in planSkillsExtractArgKeys.
func flagParityCallAction(content string, openBrace int) (string, bool) {
	depth := 0
	for i := openBrace; i < len(content); i++ {
		c := content[i]
		switch {
		case c == '"' || c == '\'' || c == '`':
			i++
			for i < len(content) && content[i] != c {
				if content[i] == '\\' {
					i++
				}
				i++
			}
		case c == '{' || c == '[' || c == '(':
			depth++
		case c == '}' || c == ']' || c == ')':
			depth--
			if depth <= 0 {
				return "", false
			}
		case depth == 1 && strings.HasPrefix(content[i:], "action") && (i == 0 || !isPlanSkillsIdentPart(content[i-1])):
			rest := strings.TrimLeft(content[i+len("action"):], " \t\r\n")
			if !strings.HasPrefix(rest, ":") {
				continue
			}
			rest = strings.TrimLeft(rest[1:], " \t\r\n")
			if strings.HasPrefix(rest, `"`) {
				if end := strings.IndexByte(rest[1:], '"'); end >= 0 {
					return rest[1 : 1+end], true
				}
			}
		}
	}
	return "", false
}

// flagParityAssertToolsResolve is the shared body of the "every tool and
// action a skill names resolves against the registry" check. It reads every
// markdown file in skills/<skill>/, requires each name({ ... }) call to name a
// registered tool, requires each action value to be in that tool's action
// enum, and requires the skill to make every call in mustCall (so a call
// regex that goes blind fails instead of passing on zero calls). A mustCall
// entry is a tool name ("validate") or a tool and action ("ship_state:deferred_list").
func flagParityAssertToolsResolve(t *testing.T, skill string, mustCall ...string) {
	t.Helper()
	reg := flagParityBuildRegistry(t)
	dir := filepath.Join(executeSkillsRepoRoot(t), "skills", skill)
	files, err := filepath.Glob(filepath.Join(dir, "*.md"))
	if err != nil || len(files) == 0 {
		t.Fatalf("glob skills/%s/*.md: matched %d files, err %v", skill, len(files), err)
	}
	sort.Strings(files)

	called := map[string]bool{}
	for _, path := range files {
		label := "skills/" + skill + "/" + filepath.Base(path)
		for _, c := range flagParityToolCalls(executeSkillsReadFile(t, path)) {
			called[c.name] = true
			if c.action != "" {
				called[c.name+":"+c.action] = true
			}
			switch enum := reg.actions[c.name]; {
			case !reg.tools[c.name]:
				t.Errorf("%s:%d: references MCP tool %q, which is not registered by any Register*Tools function", label, c.line, c.name)
			case c.action == "":
			case enum == nil:
				t.Errorf("%s:%d: %s call sets action %q, but the registered %s tool has no action enum", label, c.line, c.name, c.action, c.name)
			case !enum[c.action]:
				t.Errorf("%s:%d: %s call references action %q, which is not in the registered %s action enum", label, c.line, c.name, c.action, c.name)
			}
		}
	}
	for _, want := range mustCall {
		if !called[want] {
			t.Errorf("skills/%s: no call to %q found in skills/%s/*.md — the skill dropped it, or the call-detection regex no longer matches its call style", skill, want, skill)
		}
	}
}
