// Package skillcheck cross-checks the verify-requirements skill against the
// places that describe it: its frontmatter, the Flags table in
// docs/skills/verify-requirements.md, and the live MCP registry. It also
// checks that the skill stays read-only: the tokens of the ## DO NOT section
// must show nowhere else in SKILL.md.
//
// The shared helpers live in skillcheck_flags_test.go. The Flags table parser
// and the argument tokenizer come from skillcheck_preplan_test.go. Every
// unexported identifier below is prefixed verifyRequirements, and the declared
// Test functions are TestVerifyRequirementsSkillParity and
// TestVerifyRequirementsSkillReadOnly.
package skillcheck

import (
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
)

const (
	verifyRequirementsWantName  = "verify-requirements"
	verifyRequirementsWantModel = "opus"
)

// verifyRequirementsFrontmatterProblems returns one message for each
// frontmatter field of the skill that differs from the expected value.
func verifyRequirementsFrontmatterProblems(skill string) []string {
	var problems []string
	want := []struct{ key, value string }{
		{"name", verifyRequirementsWantName},
		{"user-invocable", "true"},
		{"model", verifyRequirementsWantModel},
	}
	for _, w := range want {
		if got := flagParityFrontmatterField(skill, w.key); got != w.value {
			problems = append(problems, "frontmatter "+w.key+" = \""+got+"\", want \""+w.value+"\"")
		}
	}
	return problems
}

// verifyRequirementsArgDiff returns the sorted arguments that only one of the
// two sets holds. An empty result means the sets are equal.
func verifyRequirementsArgDiff(docs, hint map[string]bool) []string {
	var diff []string
	for a := range docs {
		if !hint[a] {
			diff = append(diff, "docs only: "+a)
		}
	}
	for a := range hint {
		if !docs[a] {
			diff = append(diff, "argument-hint only: "+a)
		}
	}
	sort.Strings(diff)
	return diff
}

// TestVerifyRequirementsSkillParity checks that the verify-requirements skill
// agrees with its docs page and with the live MCP registry. Each subtest first
// proves on a fixture that its check fires, then runs on the real files.
func TestVerifyRequirementsSkillParity(t *testing.T) {
	pluginRoot := executeSkillsRepoRoot(t)
	skill := executeSkillsReadFile(t, filepath.Join(pluginRoot, "skills", verifyRequirementsWantName, "SKILL.md"))
	// docs/ sits beside plugins/ at the repository root.
	docsPath := filepath.Join(filepath.Dir(filepath.Dir(pluginRoot)), "docs", "skills", verifyRequirementsWantName+".md")
	docs := executeSkillsReadFile(t, docsPath)

	t.Run("frontmatter", func(t *testing.T) {
		good := "---\nname: verify-requirements\nuser-invocable: true\nmodel: opus\n---\nbody\n"
		if p := verifyRequirementsFrontmatterProblems(good); len(p) != 0 {
			t.Fatalf("fixture with correct frontmatter gives problems: %v", p)
		}
		for _, bad := range []struct{ label, fixture string }{
			{"name", "---\nname: verify\nuser-invocable: true\nmodel: opus\n---\n"},
			{"user-invocable", "---\nname: verify-requirements\nuser-invocable: false\nmodel: opus\n---\n"},
			{"model", "---\nname: verify-requirements\nuser-invocable: true\nmodel: sonnet\n---\n"},
		} {
			p := verifyRequirementsFrontmatterProblems(bad.fixture)
			if len(p) != 1 || !strings.Contains(p[0], bad.label) {
				t.Fatalf("fixture with wrong %s: problems = %v, want one problem naming %q", bad.label, p, bad.label)
			}
		}

		for _, p := range verifyRequirementsFrontmatterProblems(skill) {
			t.Errorf("verify-requirements/SKILL.md: %s", p)
		}
	})

	t.Run("docs_flags_table_agrees", func(t *testing.T) {
		hintOf := func(s string) map[string]bool { return preplanSkillsArgTokens(s) }
		hint := "--topic <name> | --file <path>"
		if d := verifyRequirementsArgDiff(hintOf("`--topic <name>` `--file <path>`"), hintOf(hint)); len(d) != 0 {
			t.Fatalf("fixture with equal tokens gives a diff: %v", d)
		}
		want := []string{"docs only: --issue"}
		if d := verifyRequirementsArgDiff(hintOf("--topic --file --issue"), hintOf(hint)); !reflect.DeepEqual(d, want) {
			t.Fatalf("fixture with an extra docs flag: diff = %v, want %v", d, want)
		}
		want = []string{"argument-hint only: --file"}
		if d := verifyRequirementsArgDiff(hintOf("--topic"), hintOf(hint)); !reflect.DeepEqual(d, want) {
			t.Fatalf("fixture with a missing docs flag: diff = %v, want %v", d, want)
		}

		hintValue := flagParityFrontmatterField(skill, "argument-hint")
		canonical := preplanSkillsArgTokens(hintValue)
		if len(canonical) == 0 {
			t.Fatalf("verify-requirements/SKILL.md argument-hint %q holds no argument", hintValue)
		}
		for _, d := range verifyRequirementsArgDiff(preplanSkillsFlagsTableArgs(t, docs), canonical) {
			t.Errorf("docs/skills/verify-requirements.md Flags table and argument-hint disagree: %s", d)
		}
	})

	t.Run("tools_and_actions_resolve", func(t *testing.T) {
		reg := flagParityBuildRegistry(t)
		if !reg.actions["plan_support"]["verify_requirements_context"] {
			t.Fatal("plan_support action enum does not hold verify_requirements_context")
		}
		// Fixtures: the registry must reject an unknown tool and an unknown action.
		if reg.tools["no_such_tool"] {
			t.Fatal("registry accepts the fixture tool no_such_tool")
		}
		if reg.actions["plan_support"]["no_such_action"] {
			t.Fatal("registry accepts the fixture action no_such_action")
		}
		flagParityAssertToolsResolve(t, verifyRequirementsWantName, "plan_support:verify_requirements_context")
	})
}

// verifyRequirementsWriteTokenRes holds one whole-word pattern for each token
// that the skill must never use outside its ## DO NOT section. The match is
// case-sensitive. The token "gh " matches a gh command only: a word boundary
// before "gh" keeps words such as "through " from matching.
var verifyRequirementsWriteTokenRes = func() map[string]*regexp.Regexp {
	words := []string{
		"Edit", "Write", "NotebookEdit", "AskUserQuestion",
		"plan_prepare", "plan_mark", "jira save",
		"preplan_context", "preplan_check", "preplan_write", "evidence_record", "openspec_stage",
		"addCommentToJiraIssue", "editJiraIssue", "transitionJiraIssue", "createJiraIssue",
	}
	res := map[string]*regexp.Regexp{}
	for _, w := range words {
		res[w] = regexp.MustCompile(`\b` + regexp.QuoteMeta(w) + `\b`)
	}
	res["gh "] = regexp.MustCompile(`\bgh\s`)
	return res
}()

// verifyRequirementsWriteTokens returns the sorted write tokens that the skill
// text holds outside its "## DO NOT" section. The section runs from the
// "## DO NOT" heading to the next "## " heading or the end of the text.
func verifyRequirementsWriteTokens(skill string) []string {
	var outside []string
	inDoNot := false
	for _, line := range strings.Split(skill, "\n") {
		if strings.HasPrefix(line, "## ") {
			inDoNot = strings.TrimSpace(strings.TrimPrefix(line, "## ")) == "DO NOT"
		}
		if !inDoNot {
			outside = append(outside, line)
		}
	}
	text := strings.Join(outside, "\n")

	var found []string
	for tok, re := range verifyRequirementsWriteTokenRes {
		if re.MatchString(text) {
			found = append(found, tok)
		}
	}
	sort.Strings(found)
	return found
}

// TestVerifyRequirementsSkillReadOnly checks that no write token of the skill
// shows outside the ## DO NOT section of verify-requirements/SKILL.md.
func TestVerifyRequirementsSkillReadOnly(t *testing.T) {
	t.Run("helper_fixtures", func(t *testing.T) {
		cases := []struct {
			label string
			input string
			want  []string
		}{
			{"token outside the section", "## Step 2\nCall preplan_write\n## DO NOT\n- preplan_write", []string{"preplan_write"}},
			{"token only inside the section", "## Step 2\nRead the file\n## DO NOT\n- preplan_write\n- `Edit`", nil},
			{"token in a later section", "## DO NOT\n- Edit\n## Notes\nUse the Edit tool", []string{"Edit"}},
			{"gh command", "## Step 1\nRun gh issue view 5\n## DO NOT\n- gh ", []string{"gh "}},
			{"through the file gives no gh", "## Step 1\nRead through the file.\n## DO NOT\n- gh ", nil},
			{"jira save", "## Step 1\nRun jira save now\n## DO NOT\n", []string{"jira save"}},
			{"jira load is allowed", "## Step 1\nRun jira load now\n## DO NOT\n", nil},
			{"NotebookEdit is not Edit", "## Step 1\nUse NotebookEdit\n## DO NOT\n", []string{"NotebookEdit"}},
			{"whole word only", "## Step 1\nEditor and Writer and preplan_writer\n## DO NOT\n", nil},
			{"atlassian write", "## Step 1\nCall editJiraIssue\n## DO NOT\n", []string{"editJiraIssue"}},
		}
		for _, c := range cases {
			if got := verifyRequirementsWriteTokens(c.input); !reflect.DeepEqual(got, c.want) {
				t.Errorf("%s: tokens = %v, want %v", c.label, got, c.want)
			}
		}
	})

	t.Run("real_skill", func(t *testing.T) {
		pluginRoot := executeSkillsRepoRoot(t)
		skill := executeSkillsReadFile(t, filepath.Join(pluginRoot, "skills", verifyRequirementsWantName, "SKILL.md"))
		if !strings.Contains(skill, "\n## DO NOT\n") {
			t.Fatal("verify-requirements/SKILL.md has no \"## DO NOT\" section")
		}
		for _, tok := range verifyRequirementsWriteTokens(skill) {
			t.Errorf("verify-requirements/SKILL.md holds the write token %q outside the ## DO NOT section", tok)
		}
	})
}
