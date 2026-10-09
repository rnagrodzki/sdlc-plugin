// Package skillcheck cross-checks the openspec-save skill across the files that
// describe it: plugins/sdlc/skills/openspec-save/SKILL.md, the user
// documentation docs/skills/openspec-save.md, and the two tables that list the
// skill (docs/skills/README.md and docs/getting-started.md). The flags in the
// argument-hint of SKILL.md are the canonical set. Every other place that
// names a flag must agree with it. The skill calls the openspec_save tool and
// runs the commit skill and the pr skill, so the test pins those three calls
// and the exact flags the skill hands to each target.
//
// The shared matching and registry helpers live in skillcheck_flags_test.go and
// skillcheck_deferred_test.go. Every unexported identifier below is prefixed
// openspecSaveSkills, and the one declared Test function is
// TestOpenspecSaveSkillParity.
package skillcheck

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/rnagrodzki/sdlc-plugin/internal/tools"
)

var (
	// openspecSaveSkillsSlashSpanRe matches an inline code span holding a
	// /sdlc:openspec-save command. Submatch 1 is the text after the command
	// name.
	openspecSaveSkillsSlashSpanRe = regexp.MustCompile("`/sdlc:openspec-save(\\s[^`]*)?`")

	// openspecSaveSkillsDocsCommandRe matches an indented (4 spaces) command
	// line in docs/skills/openspec-save.md, the way the docs show every
	// command. Submatch 1 is the text after the command name.
	openspecSaveSkillsDocsCommandRe = regexp.MustCompile(`(?m)^ {4}/openspec-save((?:\s.*)?)$`)

	// openspecSaveSkillsStepRe matches a numbered step heading of SKILL.md.
	// Submatch 1 is the step number and submatch 2 is the step title.
	openspecSaveSkillsStepRe = regexp.MustCompile(`(?m)^## Step (\d+) — (.+)$`)

	// openspecSaveSkillsAgentRe matches the notation a skill uses to dispatch
	// an Agent worker.
	openspecSaveSkillsAgentRe = regexp.MustCompile(`Agent\s*(?:→|->)`)
)

// openspecSaveSkillsDocsDir returns the docs directory of the repository.
// The docs directory sits beside plugins/ at the repository root.
func openspecSaveSkillsDocsDir(t *testing.T) string {
	t.Helper()
	return filepath.Join(filepath.Dir(filepath.Dir(executeSkillsRepoRoot(t))), "docs")
}

// openspecSaveSkillsSorted returns the members of set in sorted order.
func openspecSaveSkillsSorted(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// openspecSaveSkillsDispatchFlags returns the flags that content hands to each
// target skill, keyed by the target skill name. A target that receives no
// flag is not in the result. The usage lines of openspec-save itself are not
// dispatches, so the result leaves them out.
func openspecSaveSkillsDispatchFlags(content string, known map[string]bool) map[string]map[string]bool {
	out := map[string]map[string]bool{}
	for _, d := range flagParityExtract("openspec-save", content, known) {
		if d.to == "openspec-save" {
			continue
		}
		if out[d.to] == nil {
			out[d.to] = map[string]bool{}
		}
		out[d.to][d.flag] = true
	}
	return out
}

// openspecSaveSkillsDispatchDiff compares the flags handed to each target
// skill with the wanted flags. It returns one sorted message for each target
// whose flag set differs, and no message when both maps agree.
func openspecSaveSkillsDispatchDiff(got, want map[string]map[string]bool) []string {
	targets := map[string]bool{}
	for to := range got {
		targets[to] = true
	}
	for to := range want {
		targets[to] = true
	}
	var msgs []string
	for to := range targets {
		g, w := openspecSaveSkillsSorted(got[to]), openspecSaveSkillsSorted(want[to])
		if strings.Join(g, " ") != strings.Join(w, " ") {
			msgs = append(msgs, fmt.Sprintf("dispatch to %q: flags %v, want %v", to, g, w))
		}
	}
	sort.Strings(msgs)
	return msgs
}

// TestOpenspecSaveSkillParity checks the openspec-save skill files against each
// other, against the other skills they call, and against the registered
// openspec_save tool.
func TestOpenspecSaveSkillParity(t *testing.T) {
	skillDir := filepath.Join(executeSkillsRepoRoot(t), "skills", "openspec-save")
	docsDir := openspecSaveSkillsDocsDir(t)

	skill := executeSkillsReadFile(t, filepath.Join(skillDir, "SKILL.md"))
	body := flagParityBody(skill)
	docs := executeSkillsReadFile(t, filepath.Join(docsDir, "skills", "openspec-save.md"))

	canonical := flagParityHintFlags(skill)
	wantFlags := map[string]bool{"--plan": true, "--auto": true}

	t.Run("frontmatter", func(t *testing.T) {
		if got := flagParityFrontmatterField(skill, "name"); got != "openspec-save" {
			t.Errorf("frontmatter name = %q, want %q (the skill directory name)", got, "openspec-save")
		}
		if got := flagParityFrontmatterField(skill, "user-invocable"); got != "true" {
			t.Errorf("frontmatter user-invocable = %q, want %q", got, "true")
		}
		if got := flagParityFrontmatterField(skill, "model"); got != "sonnet" {
			t.Errorf("frontmatter model = %q, want %q", got, "sonnet")
		}
		wantHint := "<plan-path> | --plan <path> [--auto]"
		if got := flagParityFrontmatterField(skill, "argument-hint"); got != wantHint {
			t.Errorf("frontmatter argument-hint = %q, want %q", got, wantHint)
		}
		flagParityAssertSameSet(t, "openspec-save/SKILL.md argument-hint flags", canonical, wantFlags)
	})

	t.Run("skill_md_flags_agree", func(t *testing.T) {
		flagParityAssertSameSet(t, "openspec-save/SKILL.md description flags", flagParityFlagSet(flagParityFrontmatterField(skill, "description")), canonical)
		flagParityAssertSameSet(t, "openspec-save/SKILL.md Arguments table flags", flagParityTableFlags(body), canonical)

		usage := map[string]bool{}
		spans := deferredSkillsFlagsOf(openspecSaveSkillsSlashSpanRe, body)
		if len(spans) == 0 {
			t.Fatal("found no `/sdlc:openspec-save ...` command spans in openspec-save/SKILL.md")
		}
		for _, set := range spans {
			flagParityAssertSubset(t, "openspec-save/SKILL.md command span", set, canonical)
			for f := range set {
				usage[f] = true
			}
		}
		flagParityAssertSameSet(t, "openspec-save/SKILL.md command spans", usage, canonical)
	})

	t.Run("every_flag_appears_in_the_workflow", func(t *testing.T) {
		start := strings.Index(body, "## Step 1 —")
		if start < 0 {
			t.Fatal("openspec-save/SKILL.md has no `## Step 1 —` heading")
		}
		workflow := body[start:]
		for _, flag := range openspecSaveSkillsSorted(canonical) {
			if !strings.Contains(workflow, flag) {
				t.Errorf("flag %s is in the argument-hint but not in the workflow steps of openspec-save/SKILL.md", flag)
			}
		}
	})

	t.Run("docs_flags_agree", func(t *testing.T) {
		commands := deferredSkillsFlagsOf(openspecSaveSkillsDocsCommandRe, docs)
		if len(commands) == 0 {
			t.Fatal("found no indented /openspec-save command lines in docs/skills/openspec-save.md")
		}
		usage := map[string]bool{}
		for _, set := range commands {
			flagParityAssertSubset(t, "docs/skills/openspec-save.md command", set, canonical)
			for f := range set {
				usage[f] = true
			}
		}
		flagParityAssertSameSet(t, "docs/skills/openspec-save.md command lines", usage, canonical)
		flagParityAssertSameSet(t, "docs/skills/openspec-save.md flag table", flagParityTableFlags(docs), canonical)
	})

	t.Run("docs_explain_merge_first_flow", func(t *testing.T) {
		for _, want := range []string{
			"## The merge-first flow",
			"`openspec/<change>`",
			"`/ship`",
			"**Skip release (acknowledged)**",
			"`already`",
		} {
			if !strings.Contains(docs, want) {
				t.Errorf("docs/skills/openspec-save.md does not contain %q", want)
			}
		}
	})

	t.Run("skill_lists_name_the_skill", func(t *testing.T) {
		readme := executeSkillsReadFile(t, filepath.Join(docsDir, "skills", "README.md"))
		if !strings.Contains(readme, "](openspec-save.md)") {
			t.Error("docs/skills/README.md has no link to openspec-save.md")
		}
		started := executeSkillsReadFile(t, filepath.Join(docsDir, "getting-started.md"))
		if !strings.Contains(started, "`/openspec-save ") {
			t.Error("docs/getting-started.md has no `/openspec-save ...` row")
		}
	})

	t.Run("step_headings", func(t *testing.T) {
		var got []string
		for _, m := range openspecSaveSkillsStepRe.FindAllStringSubmatch(body, -1) {
			got = append(got, m[1]+" "+m[2])
		}
		want := []string{"1 Save", "2 Commit", "3 PR", "4 Report"}
		if strings.Join(got, "|") != strings.Join(want, "|") {
			t.Errorf("step headings = %v, want %v", got, want)
		}
	})

	t.Run("dispatches_pinned", func(t *testing.T) {
		known := map[string]bool{}
		for name := range flagParityLoadSkills(t) {
			known[name] = true
		}
		want := map[string]map[string]bool{
			"commit": {"--type": true, "--scope": true, "--auto": true},
			"ship":   {"--plan": true},
		}
		for _, msg := range openspecSaveSkillsDispatchDiff(openspecSaveSkillsDispatchFlags(skill, known), want) {
			t.Error(msg)
		}

		// The two Skill calls, in order: the commit call carries the flags, and
		// the pr call carries no argument at all.
		calls := flagParitySkillCalls(skill)
		if len(calls) != 2 || calls[0].to != "commit" || calls[1].to != "pr" {
			t.Fatalf("Skill calls = %+v, want one commit call and then one pr call", calls)
		}
		if args := skill[calls[1].start:calls[1].end]; args != "" {
			t.Errorf("the pr call has the argument %q, want none", args)
		}
		for _, call := range []string{
			`Skill("sdlc:commit", "--type docs --scope openspec [--auto when --auto was passed]")`,
			`Skill("sdlc:pr", "")`,
		} {
			if !strings.Contains(skill, call) {
				t.Errorf("openspec-save/SKILL.md does not contain the call %s", call)
			}
		}
		if openspecSaveSkillsAgentRe.MatchString(skill) {
			t.Error("openspec-save/SKILL.md dispatches an Agent worker; it must run the commit and pr skills in the session")
		}
	})

	t.Run("tools_and_actions_resolve", func(t *testing.T) {
		flagParityAssertToolsResolve(t, "openspec-save", "openspec_save")
	})

	t.Run("tool_input_matches_the_call", func(t *testing.T) {
		if !strings.Contains(skill, `openspec_save({ planPath: "`) {
			t.Error(`openspec-save/SKILL.md does not contain the call openspec_save({ planPath: "..." })`)
		}
		found := false
		for _, tl := range executeSkillsListTools(t) {
			if tl.Name != "openspec_save" {
				continue
			}
			found = true
			raw, err := json.Marshal(tl.InputSchema)
			if err != nil {
				t.Fatalf("marshal the openspec_save input schema: %v", err)
			}
			var schema struct {
				Properties map[string]json.RawMessage `json:"properties"`
				Required   []string                   `json:"required"`
			}
			if err := json.Unmarshal(raw, &schema); err != nil {
				t.Fatalf("unmarshal the openspec_save input schema: %v", err)
			}
			if _, ok := schema.Properties["planPath"]; !ok || len(schema.Properties) != 1 {
				t.Errorf("openspec_save input properties = %v, want only planPath", schema.Properties)
			}
			if strings.Join(schema.Required, " ") != "planPath" {
				t.Errorf("openspec_save required inputs = %v, want [planPath]", schema.Required)
			}
		}
		if !found {
			t.Fatal("openspec_save is not registered by any Register*Tools function")
		}
	})

	t.Run("result_fields_are_named", func(t *testing.T) {
		out := reflect.TypeOf(tools.OpenspecSaveOut{})
		for i := 0; i < out.NumField(); i++ {
			name := strings.Split(out.Field(i).Tag.Get("json"), ",")[0]
			if name == "" {
				t.Fatalf("OpenspecSaveOut field %s has no json tag", out.Field(i).Name)
			}
			if !strings.Contains(skill, "`"+name+"`") {
				t.Errorf("openspec-save/SKILL.md does not name the result field `%s` of openspec_save", name)
			}
		}
		for _, value := range []string{"`created`", "`already`"} {
			if !strings.Contains(skill, value) {
				t.Errorf("openspec-save/SKILL.md does not name the materialized value %s", value)
			}
		}
	})

	t.Run("claims_about_other_skills_hold", func(t *testing.T) {
		skillsDir := filepath.Join(executeSkillsRepoRoot(t), "skills")
		commit := executeSkillsReadFile(t, filepath.Join(skillsDir, "commit", "SKILL.md"))
		pr := executeSkillsReadFile(t, filepath.Join(skillsDir, "pr", "SKILL.md"))

		claims := []struct{ in, text, why string }{
			{"commit/SKILL.md", "no files staged for commit", "the commit skill stops on an empty stage, which is why the commit section is skipped"},
			{"pr/SKILL.md", "Skip release (acknowledged)", "the release-intent option the user is told to choose"},
			{"pr/SKILL.md", "cannot decide release intent", "the pr skill in auto mode stops, which is why the pr skill never gets --auto"},
			{"pr/SKILL.md", "updates the open PR for the branch", "the pr skill updates an open PR on a repeated run"},
		}
		sources := map[string]string{"commit/SKILL.md": commit, "pr/SKILL.md": pr}
		for _, c := range claims {
			if !strings.Contains(sources[c.in], c.text) {
				t.Errorf("%s no longer contains %q: %s", c.in, c.text, c.why)
			}
		}
		for _, text := range []string{"no files staged for commit", "Skip release (acknowledged)", "updates it"} {
			if !strings.Contains(skill, text) {
				t.Errorf("openspec-save/SKILL.md does not contain %q", text)
			}
		}
	})

	t.Run("routes_and_reentry_are_written", func(t *testing.T) {
		for _, want := range []string{
			"## Routes",
			"## Run the skill again",
			"`stagedFiles` is `(none)`",
			"Skip Step 2",
			"Do not route on `next`",
			"git branch --show-current",
			"Never pass `--auto` to the pr skill.",
			"Never infer it",
			"`openspec-save: <plan-path> and --plan name different files.`",
		} {
			if !strings.Contains(skill, want) {
				t.Errorf("openspec-save/SKILL.md does not contain %q", want)
			}
		}
	})

	t.Run("dispatch_helpers", func(t *testing.T) {
		known := map[string]bool{"commit": true, "pr": true, "openspec-save": true}
		fixture := "Skill(\"sdlc:commit\", \"--type docs --scope openspec\")\nSkill(\"sdlc:pr\", \"--auto\")\nSkill(\"sdlc:commit\", \"--auto\")\nUsage: `/sdlc:openspec-save --plan x --auto`\n"
		got := openspecSaveSkillsDispatchFlags(fixture, known)
		wantGot := map[string]map[string]bool{
			"commit": {"--type": true, "--scope": true, "--auto": true},
			"pr":     {"--auto": true},
		}
		if diff := openspecSaveSkillsDispatchDiff(got, wantGot); len(diff) != 0 {
			t.Errorf("dispatch flags of the fixture differ from the expected flags: %v", diff)
		}

		cases := []struct {
			name string
			got  map[string]map[string]bool
			want map[string]map[string]bool
			msgs []string
		}{
			{
				name: "agree",
				got:  map[string]map[string]bool{"commit": {"--type": true}},
				want: map[string]map[string]bool{"commit": {"--type": true}},
			},
			{
				name: "unexpected_target",
				got:  map[string]map[string]bool{"commit": {"--type": true}, "pr": {"--auto": true}},
				want: map[string]map[string]bool{"commit": {"--type": true}},
				msgs: []string{`dispatch to "pr": flags [--auto], want []`},
			},
			{
				name: "missing_flag",
				got:  map[string]map[string]bool{"commit": {"--type": true}},
				want: map[string]map[string]bool{"commit": {"--type": true, "--scope": true}},
				msgs: []string{`dispatch to "commit": flags [--type], want [--scope --type]`},
			},
			{
				name: "missing_target",
				got:  map[string]map[string]bool{},
				want: map[string]map[string]bool{"ship": {"--plan": true}},
				msgs: []string{`dispatch to "ship": flags [], want [--plan]`},
			},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				msgs := openspecSaveSkillsDispatchDiff(tc.got, tc.want)
				if strings.Join(msgs, "\n") != strings.Join(tc.msgs, "\n") {
					t.Errorf("messages = %q, want %q", msgs, tc.msgs)
				}
			})
		}
	})
}
