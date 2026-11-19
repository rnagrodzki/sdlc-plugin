package tools

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/rnagrodzki/sdlc-plugin/internal/config"
	"github.com/rnagrodzki/sdlc-plugin/internal/fsx"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/setupmeta"
)

// saveRepo makes a repo root with config.toml (always, so the state is
// ready) and local.toml (when local is not empty). The user file path points
// at a file under a second temp dir; userContent is written there when it is
// not empty.
func saveRepo(t *testing.T, configTOML, localTOML, userContent string) (root, userPath string) {
	t.Helper()
	root, userPath = dashSetupRoot(t)
	dashSetupWrite(t, saveConfigPath(root), configTOML)
	if localTOML != "" {
		dashSetupWrite(t, saveLocalPath(root), localTOML)
	}
	if userContent != "" {
		dashSetupWrite(t, userPath, userContent)
	}
	return root, userPath
}

func saveConfigPath(root string) string { return filepath.Join(root, ".sdlc-v2", "config.toml") }
func saveLocalPath(root string) string  { return filepath.Join(root, ".sdlc-v2", "local.toml") }

// saveDoc decodes the TOML file at path. An absent file gives nil.
func saveDoc(t *testing.T, path string) map[string]any {
	t.Helper()
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	var doc map[string]any
	if err := fsx.ReadTOML(path, &doc); err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return doc
}

// saveValue returns the value at the dotted path in the TOML file at path.
func saveValue(t *testing.T, path, dotted string) (any, bool) {
	t.Helper()
	return setupLookup(saveDoc(t, path), strings.Split(dotted, "."))
}

// saveSnapshot returns the content of each path; "<absent>" for a missing file.
func saveSnapshot(paths ...string) map[string]string {
	out := map[string]string{}
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			out[p] = "<absent>"
			continue
		}
		out[p] = string(b)
	}
	return out
}

// saveOK calls DashboardSetupSave and fails the test on an error or ok false.
func saveOK(t *testing.T, root string, in DashboardSetupSaveIn) DashboardSetupSaveOut {
	t.Helper()
	out, err := DashboardSetupSave(root, in)
	if err != nil {
		t.Fatalf("DashboardSetupSave: %v", err)
	}
	if !out.OK {
		t.Fatalf("DashboardSetupSave: ok false: %+v", out)
	}
	return out
}

func saveSet(section, field string, value any) DashboardSetupChange {
	return DashboardSetupChange{Section: section, Field: field, Op: "set", Value: value}
}

func saveReset(section, field string) DashboardSetupChange {
	return DashboardSetupChange{Section: section, Field: field, Op: "reset"}
}

// TestDashboardSetupSave_SaveRules covers each row of the save rule table.
func TestDashboardSetupSave_SaveRules(t *testing.T) {
	t.Run("config.toml section", func(t *testing.T) {
		root, _ := saveRepo(t, "[jira]\ndefaultProject = \"OLD\"\nprojects = [\"A\", \"B\"]\n", "", "")
		out := saveOK(t, root, DashboardSetupSaveIn{Changes: []DashboardSetupChange{saveSet("jira", "defaultProject", "NEW")}})
		want := []DashboardSetupWrite{{Section: "jira", File: saveConfigPath(root)}}
		if !slices.Equal(out.Written, want) {
			t.Errorf("written = %+v, want %+v", out.Written, want)
		}
		if v, _ := saveValue(t, saveConfigPath(root), "jira.defaultProject"); v != "NEW" {
			t.Errorf("jira.defaultProject = %v, want NEW", v)
		}
		if v, ok := saveValue(t, saveConfigPath(root), "jira.projects"); !ok || fmt.Sprint(v) != "[A B]" {
			t.Errorf("jira.projects = %v, want it kept", v)
		}
		saveOK(t, root, DashboardSetupSaveIn{Changes: []DashboardSetupChange{saveReset("jira", "defaultProject")}})
		if _, ok := saveValue(t, saveConfigPath(root), "jira.defaultProject"); ok {
			t.Error("jira.defaultProject still set after reset")
		}
		if _, ok := saveValue(t, saveConfigPath(root), "jira.projects"); !ok {
			t.Error("jira.projects removed by the reset")
		}
	})

	t.Run("this project", func(t *testing.T) {
		root, userPath := saveRepo(t, "", "[ship]\nauto = true\n", "[ship]\nbump = \"major\"\n")
		before := saveSnapshot(userPath)
		out := saveOK(t, root, DashboardSetupSaveIn{
			Changes: []DashboardSetupChange{saveSet("ship", "bump", "minor")},
			Targets: map[string]string{"ship": "project"},
		})
		want := []DashboardSetupWrite{{Section: "ship", File: saveLocalPath(root)}}
		if !slices.Equal(out.Written, want) {
			t.Errorf("written = %+v, want %+v", out.Written, want)
		}
		if v, _ := saveValue(t, saveLocalPath(root), "ship.bump"); v != "minor" {
			t.Errorf("project ship.bump = %v, want minor", v)
		}
		if v, _ := saveValue(t, saveLocalPath(root), "ship.auto"); v != true {
			t.Errorf("project ship.auto = %v, want it kept", v)
		}
		saveOK(t, root, DashboardSetupSaveIn{
			Changes: []DashboardSetupChange{saveReset("ship", "bump")},
			Targets: map[string]string{"ship": "project"},
		})
		if _, ok := saveValue(t, saveLocalPath(root), "ship.bump"); ok {
			t.Error("project ship.bump still set after reset")
		}
		if got := saveSnapshot(userPath); got[userPath] != before[userPath] {
			t.Errorf("user file changed:\n%s", got[userPath])
		}
	})

	t.Run("all my projects", func(t *testing.T) {
		root, userPath := saveRepo(t, "",
			"[ship]\nbump = \"patch\"\n\n[ship.steps]\nexecute = true\n",
			"[ship]\ndraft = true\n")
		out := saveOK(t, root, DashboardSetupSaveIn{
			Changes: []DashboardSetupChange{saveSet("ship", "bump", "minor"), saveReset("ship", "draft")},
			Targets: map[string]string{"ship": "user"},
		})
		want := []DashboardSetupWrite{{Section: "ship", File: userPath}, {Section: "ship", File: saveLocalPath(root)}}
		if !slices.Equal(out.Written, want) {
			t.Errorf("written = %+v, want %+v", out.Written, want)
		}
		if v, _ := saveValue(t, userPath, "ship.bump"); v != "minor" {
			t.Errorf("user ship.bump = %v, want minor", v)
		}
		if _, ok := saveValue(t, userPath, "ship.draft"); ok {
			t.Error("user ship.draft still set")
		}
		if _, ok := saveValue(t, saveLocalPath(root), "ship.bump"); ok {
			t.Error("project ship.bump still set")
		}
		if v, _ := saveValue(t, saveLocalPath(root), "ship.steps.execute"); v != true {
			t.Errorf("project ship.steps = %v, want it kept", v)
		}
	})

	t.Run("all my projects, use default", func(t *testing.T) {
		root, userPath := saveRepo(t, "", "[ship]\nbump = \"patch\"\n", "[ship]\nbump = \"major\"\n")
		saveOK(t, root, DashboardSetupSaveIn{
			Changes: []DashboardSetupChange{saveReset("ship", "bump")},
			Targets: map[string]string{"ship": "user"},
		})
		if _, ok := saveValue(t, userPath, "ship.bump"); ok {
			t.Error("user ship.bump still set")
		}
		if _, ok := saveValue(t, saveLocalPath(root), "ship.bump"); ok {
			t.Error("project ship.bump still set")
		}
	})

	t.Run("absent target uses the default target", func(t *testing.T) {
		root, userPath := saveRepo(t, "", "", "")
		out := saveOK(t, root, DashboardSetupSaveIn{Changes: []DashboardSetupChange{saveSet("review", "scope", "staged")}})
		want := []DashboardSetupWrite{{Section: "review", File: userPath}}
		if !slices.Equal(out.Written, want) {
			t.Errorf("written = %+v, want %+v", out.Written, want)
		}
		if v, _ := saveValue(t, userPath, "review.scope"); v != "staged" {
			t.Errorf("user review.scope = %v, want staged", v)
		}
		if _, err := os.Stat(saveLocalPath(root)); !errors.Is(err, os.ErrNotExist) {
			t.Error("project file was created with no project change")
		}
	})
}

// TestDashboardSetupSave_SectionIDs checks that the save writes the config
// path of each form section and that the result names the form section ids.
func TestDashboardSetupSave_SectionIDs(t *testing.T) {
	root, _ := saveRepo(t, "", "", "")
	out := saveOK(t, root, DashboardSetupSaveIn{
		Changes: []DashboardSetupChange{
			saveSet("plan-tasks", "contractShape", "minimal"),
			saveSet("communication-style", "tone", "neutral"),
		},
		Targets: map[string]string{"communication-style": "project"},
	})
	want := []DashboardSetupWrite{
		{Section: "communication-style", File: saveLocalPath(root)},
		{Section: "plan-tasks", File: saveConfigPath(root)},
	}
	slices.SortFunc(out.Written, func(a, b DashboardSetupWrite) int { return strings.Compare(a.Section, b.Section) })
	if !slices.Equal(out.Written, want) {
		t.Errorf("written = %+v, want %+v", out.Written, want)
	}
	if v, _ := saveValue(t, saveConfigPath(root), "plan.tasks.contractShape"); v != "minimal" {
		t.Errorf("plan.tasks.contractShape = %v, want minimal", v)
	}
	if v, _ := saveValue(t, saveLocalPath(root), "style.tone"); v != "neutral" {
		t.Errorf("style.tone = %v, want neutral", v)
	}
}

// TestDashboardSetupSave_DottedField checks that a dotted field name changes
// the nested key, and that the value check runs on it.
func TestDashboardSetupSave_DottedField(t *testing.T) {
	root, userPath := saveRepo(t, "[version]\nmethod = \"push\"\n\n[version.tag]\nenabled = true\nprefix = \"v\"\n", "", "[automation.drift]\nmaxErrorRate = 0.1\n")
	saveOK(t, root, DashboardSetupSaveIn{Changes: []DashboardSetupChange{
		saveSet("version", "tag.prefix", "r"),
		saveSet("automation", "drift.minErrorFloor", float64(5)),
	}})
	if v, _ := saveValue(t, saveConfigPath(root), "version.tag.prefix"); v != "r" {
		t.Errorf("version.tag.prefix = %v, want r", v)
	}
	if v, _ := saveValue(t, saveConfigPath(root), "version.tag.enabled"); v != true {
		t.Errorf("version.tag.enabled = %v, want it kept", v)
	}
	if v, _ := saveValue(t, userPath, "automation.drift.minErrorFloor"); fmt.Sprint(v) != "5" {
		t.Errorf("automation.drift.minErrorFloor = %v, want 5", v)
	}
	if v, _ := saveValue(t, userPath, "automation.drift.maxErrorRate"); fmt.Sprint(v) != "0.1" {
		t.Errorf("automation.drift.maxErrorRate = %v, want it kept", v)
	}
	for _, path := range []string{saveConfigPath(root), userPath} {
		for key, value := range saveDoc(t, path) {
			if table, ok := value.(map[string]any); ok {
				for k := range table {
					if strings.Contains(k, ".") {
						t.Errorf("%s: [%s] holds the flat dotted key %q", path, key, k)
					}
				}
			}
		}
	}

	before := saveSnapshot(saveConfigPath(root), userPath)
	_, err := DashboardSetupSave(root, DashboardSetupSaveIn{Changes: []DashboardSetupChange{
		saveSet("version", "tag.prefix", strings.Repeat("x", maxSetupTextBytes+1)),
		saveSet("automation", "drift.minErrorFloor", float64(-1)),
	}})
	var invalid *DashboardSetupInvalid
	if !errors.As(err, &invalid) {
		t.Fatalf("err = %v, want *DashboardSetupInvalid", err)
	}
	var paths []string
	for _, f := range invalid.Fields {
		paths = append(paths, f.Path)
	}
	if want := []string{"automation.drift.minErrorFloor", "version.tag.prefix"}; !slices.Equal(paths, want) {
		t.Errorf("refused paths = %v, want %v", paths, want)
	}
	if got := saveSnapshot(saveConfigPath(root), userPath); fmt.Sprint(got) != fmt.Sprint(before) {
		t.Error("a refused save changed a file")
	}
}

// saveLineDiff returns the lines of old that are not in new, and the lines of
// new that are not in old, by a greedy in-order match.
func saveLineDiff(oldText, newText string) (removed, added []string) {
	oldLines := strings.Split(oldText, "\n")
	newLines := strings.Split(newText, "\n")
	used := make([]bool, len(newLines))
	pos := 0
	for _, line := range oldLines {
		found := -1
		for j := pos; j < len(newLines); j++ {
			if newLines[j] == line {
				found = j
				break
			}
		}
		if found < 0 {
			removed = append(removed, line)
			continue
		}
		used[found] = true
		pos = found + 1
	}
	for j, line := range newLines {
		if !used[j] {
			added = append(added, line)
		}
	}
	return removed, added
}

// TestDashboardSetupSave_KeepsTemplateComments checks that a save into the
// seeded templates keeps each comment line and the key order: every line that
// the save did not change stays, in the same order.
func TestDashboardSetupSave_KeepsTemplateComments(t *testing.T) {
	root, _ := saveRepo(t, configTemplate, localTemplate, "")
	saveOK(t, root, DashboardSetupSaveIn{
		Changes: []DashboardSetupChange{
			saveSet("jira", "defaultProject", "ABC"),
			saveSet("version", "tag.prefix", "r"),
			saveSet("ship", "bump", "minor"),
		},
		Targets: map[string]string{"ship": "project"},
	})
	cases := []struct {
		path, template string
		removed, added []string
	}{
		{saveConfigPath(root), configTemplate,
			[]string{`prefix = "v"`, `defaultProject = ""`},
			[]string{`prefix = 'r'`, `defaultProject = 'ABC'`}},
		// The write code puts the value in place of the commented template
		// line of the same key.
		{saveLocalPath(root), localTemplate, []string{`# bump = "patch"`}, []string{`bump = 'minor'`}},
	}
	for _, tc := range cases {
		got, err := os.ReadFile(tc.path)
		if err != nil {
			t.Fatal(err)
		}
		removed, added := saveLineDiff(tc.template, string(got))
		if !slices.Equal(removed, tc.removed) || !slices.Equal(added, tc.added) {
			t.Errorf("%s: removed %q, added %q; want removed %q, added %q", tc.path, removed, added, tc.removed, tc.added)
		}
	}
}

// saveFormSections returns the sections of setupmeta.Sections() that the form
// can change: not delegated, with a config path and with fields.
func saveFormSections() []setupmeta.Section {
	var out []setupmeta.Section
	for _, sec := range setupmeta.Sections() {
		if sec.DelegatedTo == "" && sec.ConfigPath != "" && len(sec.Fields) > 0 {
			out = append(out, sec)
		}
	}
	return out
}

// saveFormSectionIDs returns the ids of saveFormSections, joined by ", ".
func saveFormSectionIDs() string {
	var ids []string
	for _, sec := range saveFormSections() {
		ids = append(ids, sec.ID)
	}
	return strings.Join(ids, ", ")
}

// saveFieldNames returns the field names of the form section id, joined by
// ", ".
func saveFieldNames(t *testing.T, id string) string {
	t.Helper()
	for _, sec := range saveFormSections() {
		if sec.ID == id {
			var names []string
			for _, f := range sec.Fields {
				names = append(names, f.Name)
			}
			return strings.Join(names, ", ")
		}
	}
	t.Fatalf("no form section %q", id)
	return ""
}

// saveRequestMsg is the Msg of the DomainError of a broken request rule.
func saveRequestMsg(rule string) string {
	return "dashboard setup save: " + rule + ". Nothing was written."
}

// TestDashboardSetupSave_RequestRules covers each row of the request rule
// table. Each case returns a DomainError with the exact Msg and Suggestion of
// the rule, and no file changes.
func TestDashboardSetupSave_RequestRules(t *testing.T) {
	bad := DashboardSetupChange{Section: "nope", Field: "x", Op: "bad"}
	tooMany := slices.Repeat([]DashboardSetupChange{bad}, maxSetupChanges+1)
	// Exactly maxSetupChanges entries pass the count rule, so the next rule
	// (the section of the first change) refuses the request.
	atLimit := slices.Repeat([]DashboardSetupChange{bad}, maxSetupChanges)
	unknownSection := fmt.Sprintf(`section "nope" is not a form section. Form sections: %s`, saveFormSectionIDs())
	emptySuggestion := "Change a field or a guardrail, then save."
	cases := []struct {
		name       string
		in         DashboardSetupSaveIn
		rule       string
		suggestion string // "" means the common request Suggestion
	}{
		{"absent changes", DashboardSetupSaveIn{}, "changes and guardrails are empty", emptySuggestion},
		{"empty changes", DashboardSetupSaveIn{Changes: []DashboardSetupChange{}}, "changes and guardrails are empty", emptySuggestion},
		{"empty changes and empty guardrails", DashboardSetupSaveIn{Changes: []DashboardSetupChange{}, Guardrails: map[string][]DashboardSetupGuardrailChange{}}, "changes and guardrails are empty", emptySuggestion},
		{"too many changes", DashboardSetupSaveIn{Changes: tooMany}, fmt.Sprintf("changes has %d entries, over the limit of %d", maxSetupChanges+1, maxSetupChanges), ""},
		{"changes at the limit", DashboardSetupSaveIn{Changes: atLimit}, unknownSection, ""},
		{"unknown section", DashboardSetupSaveIn{Changes: []DashboardSetupChange{saveSet("nope", "x", "y")}}, unknownSection, ""},
		{"delegated section", DashboardSetupSaveIn{Changes: []DashboardSetupChange{saveSet("commit", "allowedTypes", []any{"feat"})}},
			fmt.Sprintf(`section "commit" is a delegated section; the form cannot change it. Form sections: %s`, saveFormSectionIDs()), ""},
		{"unknown field", DashboardSetupSaveIn{Changes: []DashboardSetupChange{saveSet("ship", "nope", "y")}},
			fmt.Sprintf(`field "nope" is not a field of section "ship". Fields: %s`, saveFieldNames(t, "ship")), ""},
		{"same field twice", DashboardSetupSaveIn{Changes: []DashboardSetupChange{saveSet("ship", "bump", "minor"), saveReset("ship", "bump")}}, `section "ship" field "bump" is in changes more than once`, ""},
		{"bad op", DashboardSetupSaveIn{Changes: []DashboardSetupChange{{Section: "ship", Field: "bump", Op: "delete"}}}, `op "delete" is not set or reset`, ""},
		{"set with no value", DashboardSetupSaveIn{Changes: []DashboardSetupChange{{Section: "ship", Field: "bump", Op: "set"}}}, `section "ship" field "bump" has op set with no value; use op reset to remove a value`, ""},
		{"reset with a value", DashboardSetupSaveIn{Changes: []DashboardSetupChange{{Section: "ship", Field: "bump", Op: "reset", Value: "minor"}}}, `section "ship" field "bump" has op reset with a value; op reset takes no value`, ""},
		{"target of a config.toml section", DashboardSetupSaveIn{Changes: []DashboardSetupChange{saveSet("jira", "defaultProject", "A")}, Targets: map[string]string{"jira": "user"}}, `targets names section "jira", which is not a local form section with a change`, ""},
		{"target with no change", DashboardSetupSaveIn{Changes: []DashboardSetupChange{saveSet("ship", "bump", "minor")}, Targets: map[string]string{"review": "user"}}, `targets names section "review", which is not a local form section with a change`, ""},
		{"bad target value", DashboardSetupSaveIn{Changes: []DashboardSetupChange{saveSet("ship", "bump", "minor")}, Targets: map[string]string{"ship": "both"}}, `target "both" is not project or user`, ""},
		// Two bad targets: the ids are checked in sorted order, so the first
		// id (automation, no change) is reported, not the bad value of ship.
		{"two bad targets", DashboardSetupSaveIn{Changes: []DashboardSetupChange{saveSet("ship", "bump", "minor")}, Targets: map[string]string{"ship": "both", "automation": "user"}}, `targets names section "automation", which is not a local form section with a change`, ""},
		{"two targets with no change", DashboardSetupSaveIn{Changes: []DashboardSetupChange{saveSet("ship", "bump", "minor")}, Targets: map[string]string{"review": "user", "github": "project"}}, `targets names section "github", which is not a local form section with a change`, ""},
		// A guardrail section stays a delegated section for a form change, and
		// targets cannot name it: guardrails go only to config.toml.
		{"form change of a guardrail section", DashboardSetupSaveIn{Changes: []DashboardSetupChange{saveSet("plan-guardrails", "dry", "x")}},
			fmt.Sprintf(`section "plan-guardrails" is a delegated section; the form cannot change it. Form sections: %s`, saveFormSectionIDs()), ""},
		{"target of a guardrail section", DashboardSetupSaveIn{
			Guardrails: map[string][]DashboardSetupGuardrailChange{"plan-guardrails": {{ID: "dry", Op: "remove"}}},
			Targets:    map[string]string{"plan-guardrails": "project"},
		}, `targets names section "plan-guardrails", which is not a local form section with a change`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root, userPath := saveRepo(t, "[jira]\ndefaultProject = \"X\"\n", "[ship]\nbump = \"patch\"\n", "[ship]\nbump = \"major\"\n")
			before := saveSnapshot(saveConfigPath(root), saveLocalPath(root), userPath)
			_, err := DashboardSetupSave(root, tc.in)
			var de *mcpserver.DomainError
			if !errors.As(err, &de) {
				t.Fatalf("err = %v, want a DomainError", err)
			}
			if want := saveRequestMsg(tc.rule); de.Msg != want {
				t.Errorf("msg = %q, want %q", de.Msg, want)
			}
			want := tc.suggestion
			if want == "" {
				want = "The page sent a request that breaks this rule. Reload the Setup tab, then save again."
			}
			if de.Suggestion != want {
				t.Errorf("suggestion = %q, want %q", de.Suggestion, want)
			}
			if got := saveSnapshot(saveConfigPath(root), saveLocalPath(root), userPath); fmt.Sprint(got) != fmt.Sprint(before) {
				t.Error("a refused request changed a file")
			}
		})
	}

	t.Run("target user with no user file path", func(t *testing.T) {
		root, _ := saveRepo(t, "", "[ship]\nbump = \"patch\"\n", "")
		t.Setenv(config.UserConfigPathEnv, "")
		t.Setenv("HOME", "")
		t.Setenv("USERPROFILE", "")
		t.Setenv("home", "")
		before := saveSnapshot(saveLocalPath(root))
		_, err := DashboardSetupSave(root, DashboardSetupSaveIn{
			Changes: []DashboardSetupChange{saveSet("ship", "bump", "minor")},
			Targets: map[string]string{"ship": "user"},
		})
		var de *mcpserver.DomainError
		if !errors.As(err, &de) || !strings.Contains(de.Msg, config.UserConfigPathEnv) {
			t.Fatalf("err = %v, want a DomainError that names %s", err, config.UserConfigPathEnv)
		}
		if got := saveSnapshot(saveLocalPath(root)); fmt.Sprint(got) != fmt.Sprint(before) {
			t.Error("a refused request changed a file")
		}
	})
}

// saveFieldValue returns a valid value for the field f.
func saveFieldValue(sec setupmeta.Section, f setupmeta.Field) any {
	switch f.Type {
	case "boolean":
		return true
	case "enum":
		return f.Options[0]
	case "multi-enum", "flag-set":
		return []any{f.Options[0]}
	case "number":
		if f.Min != nil {
			return float64(*f.Min)
		}
		return float64(1)
	case "list":
		return []any{"a"}
	}
	if sec.ConfigPath == "version" && f.Name == "pushAuth.secretName" {
		return "RELEASE_TOKEN"
	}
	if setupRateFields[sec.ConfigPath+"."+f.Name] {
		return float64(0.5)
	}
	return "x"
}

// TestDashboardSetupSave_AllFields checks that a save of every form field of
// setupmeta.Sections() passes the request rules and the value check, and
// writes each form section one time.
func TestDashboardSetupSave_AllFields(t *testing.T) {
	root, _ := saveRepo(t, "", "", "")
	var changes []DashboardSetupChange
	var wantIDs []string
	for _, sec := range saveFormSections() {
		wantIDs = append(wantIDs, sec.ID)
		for _, f := range sec.Fields {
			changes = append(changes, saveSet(sec.ID, f.Name, saveFieldValue(sec, f)))
		}
	}
	if len(changes) == 0 || len(wantIDs) == 0 {
		t.Fatal("setupmeta.Sections() has no form field")
	}
	out := saveOK(t, root, DashboardSetupSaveIn{Changes: changes})
	var gotIDs []string
	for _, w := range out.Written {
		gotIDs = append(gotIDs, w.Section)
	}
	slices.Sort(gotIDs)
	slices.Sort(wantIDs)
	if !slices.Equal(gotIDs, wantIDs) {
		t.Errorf("written sections = %v, want each form section one time: %v", gotIDs, wantIDs)
	}
}

// TestDashboardSetupSave_MaxChangesCoversFields checks that maxSetupChanges
// is not below the count of the form fields: a save of each form field one
// time passes the count rule.
func TestDashboardSetupSave_MaxChangesCoversFields(t *testing.T) {
	n := 0
	for _, sec := range saveFormSections() {
		n += len(sec.Fields)
	}
	if n > maxSetupChanges {
		t.Errorf("the form has %d fields, over maxSetupChanges %d", n, maxSetupChanges)
	}
}

// TestDashboardSetupSave_Invalid checks the error of a save with refused
// values: one entry for each field, and no file change.
func TestDashboardSetupSave_Invalid(t *testing.T) {
	root, userPath := saveRepo(t, "", "[ship]\nbump = \"patch\"\n", "")
	before := saveSnapshot(saveConfigPath(root), saveLocalPath(root), userPath)
	_, err := DashboardSetupSave(root, DashboardSetupSaveIn{
		Changes: []DashboardSetupChange{saveSet("ship", "bump", "huge"), saveSet("ship", "draft", "yes")},
		Targets: map[string]string{"ship": "project"},
	})
	var invalid *DashboardSetupInvalid
	if !errors.As(err, &invalid) {
		t.Fatalf("err = %v, want *DashboardSetupInvalid", err)
	}
	if len(invalid.Fields) != 2 || invalid.Fields[0].Section != "ship" || invalid.Fields[0].Field != "bump" || invalid.Fields[1].Field != "draft" {
		t.Errorf("fields = %+v, want ship bump and ship draft", invalid.Fields)
	}
	if !strings.HasPrefix(err.Error(), "dashboard setup save: 2 field values are refused. ship.bump: ") {
		t.Errorf("Error() = %q", err.Error())
	}
	if got := saveSnapshot(saveConfigPath(root), saveLocalPath(root), userPath); fmt.Sprint(got) != fmt.Sprint(before) {
		t.Error("a refused save changed a file")
	}
}

// TestDashboardSetupSave_NotReady checks the refusal of a save in a repo
// with no config and in a repo with an old JSON config.
func TestDashboardSetupSave_NotReady(t *testing.T) {
	cases := []struct {
		state, suggestion string
		seed              func(t *testing.T, root string)
	}{
		{SetupStateNoConfig, "Select Create config on the Setup tab, or run /sdlc:setup in the repo. Then save again.", func(*testing.T, string) {}},
		{SetupStateLegacy, "Run /sdlc:setup --migrate in the repo. Then save again.", func(t *testing.T, root string) {
			dashSetupWrite(t, filepath.Join(root, ".sdlc-v2", "config.json"), "{}")
		}},
	}
	for _, tc := range cases {
		t.Run(tc.state, func(t *testing.T) {
			root, _ := dashSetupRoot(t)
			tc.seed(t, root)
			_, err := DashboardSetupSave(root, DashboardSetupSaveIn{Changes: []DashboardSetupChange{saveSet("jira", "defaultProject", "A")}})
			var nr *DashboardSetupNotReady
			if !errors.As(err, &nr) {
				t.Fatalf("err = %v, want *DashboardSetupNotReady", err)
			}
			if nr.State != tc.state || nr.Suggestion != tc.suggestion {
				t.Errorf("got %+v, want state %q suggestion %q", nr, tc.state, tc.suggestion)
			}
			if !strings.Contains(nr.Error(), tc.state) {
				t.Errorf("Error() = %q does not name the state", nr.Error())
			}
			if _, err := os.Stat(saveConfigPath(root)); !errors.Is(err, os.ErrNotExist) {
				t.Error("config.toml was created")
			}
		})
	}
}

// replaceSetupWrite replaces setupWriteFunc for the test. fn gets the call
// number, from 1.
func replaceSetupWrite(t *testing.T, fn func(call int, contentRoot, stateRoot string, in SetupWriteSectionsIn) (SetupWriteSectionsOut, error)) *[]SetupWriteSectionsIn {
	t.Helper()
	orig := setupWriteFunc
	t.Cleanup(func() { setupWriteFunc = orig })
	var calls []SetupWriteSectionsIn
	setupWriteFunc = func(contentRoot, stateRoot string, in SetupWriteSectionsIn) (SetupWriteSectionsOut, error) {
		calls = append(calls, in)
		return fn(len(calls), contentRoot, stateRoot, in)
	}
	return &calls
}

// TestDashboardSetupSave_UserWriteFails checks that a failed user file write
// of a section leaves the key of that section in the project file.
func TestDashboardSetupSave_UserWriteFails(t *testing.T) {
	root, userPath := saveRepo(t, "", "[ship]\nbump = \"patch\"\n", "")
	calls := replaceSetupWrite(t, func(call int, contentRoot, stateRoot string, in SetupWriteSectionsIn) (SetupWriteSectionsOut, error) {
		if in.Target == "user" {
			return SetupWriteSectionsOut{OK: false, Written: nil, Errors: []string{"section ship: disk full"}, sectionErrs: map[string]string{"ship": "disk full"}}, nil
		}
		return setupWriteSections(contentRoot, stateRoot, in)
	})
	before := saveSnapshot(saveLocalPath(root))
	out, err := DashboardSetupSave(root, DashboardSetupSaveIn{
		Changes: []DashboardSetupChange{saveSet("ship", "bump", "minor")},
		Targets: map[string]string{"ship": "user"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.OK {
		t.Error("ok = true, want false")
	}
	wantFailed := []DashboardSetupFailure{{Section: "ship", File: userPath, Message: "disk full"}}
	if !slices.Equal(out.Failed, wantFailed) || len(out.Written) != 0 {
		t.Errorf("failed = %+v, written = %+v; want failed %+v", out.Failed, out.Written, wantFailed)
	}
	if len(*calls) != 1 {
		t.Errorf("write calls = %d, want 1: the project section is not sent", len(*calls))
	}
	if got := saveSnapshot(saveLocalPath(root)); fmt.Sprint(got) != fmt.Sprint(before) {
		t.Error("the project file changed")
	}
}

// TestDashboardSetupSave_SecondWriteFails checks the result of a save whose
// project file write fails after the user file write: each failed section
// gets the message of the error (with the cause text when the message does
// not hold it) and the Suggestion of the error.
func TestDashboardSetupSave_SecondWriteFails(t *testing.T) {
	const suggestion = "Free disk space, then save again."
	cases := []struct {
		name        string
		err         error
		wantMessage string
	}{
		{"cause not in the message",
			&mcpserver.InfraError{Msg: "write refused", Suggestion: suggestion, Cause: errors.New("disk full")},
			"write refused: disk full"},
		{"cause in the message",
			&mcpserver.DataError{Msg: "write refused: disk full", Suggestion: suggestion, Cause: errors.New("disk full")},
			"write refused: disk full"},
		{"no cause",
			&mcpserver.InfraError{Msg: "write refused", Suggestion: suggestion},
			"write refused"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root, userPath := saveRepo(t, "[jira]\ndefaultProject = \"X\"\n", "[ship]\nbump = \"patch\"\n", "")
			replaceSetupWrite(t, func(call int, contentRoot, stateRoot string, in SetupWriteSectionsIn) (SetupWriteSectionsOut, error) {
				if call == 2 {
					return SetupWriteSectionsOut{}, tc.err
				}
				return setupWriteSections(contentRoot, stateRoot, in)
			})
			out, err := DashboardSetupSave(root, DashboardSetupSaveIn{
				Changes: []DashboardSetupChange{saveSet("ship", "bump", "minor"), saveSet("jira", "defaultProject", "Y")},
				Targets: map[string]string{"ship": "user"},
			})
			if err != nil {
				t.Fatal(err)
			}
			if out.OK {
				t.Error("ok = true, want false")
			}
			if want := []DashboardSetupWrite{{Section: "ship", File: userPath}}; !slices.Equal(out.Written, want) {
				t.Errorf("written = %+v, want %+v", out.Written, want)
			}
			wantFailed := []DashboardSetupFailure{
				{Section: "jira", File: saveConfigPath(root), Message: tc.wantMessage, Suggestion: suggestion},
				{Section: "ship", File: saveLocalPath(root), Message: tc.wantMessage, Suggestion: suggestion},
			}
			if !slices.Equal(out.Failed, wantFailed) {
				t.Errorf("failed = %+v, want %+v", out.Failed, wantFailed)
			}
			if v, _ := saveValue(t, saveLocalPath(root), "ship.bump"); v != "patch" {
				t.Errorf("project ship.bump = %v, want the old value patch", v)
			}
			if v, _ := saveValue(t, userPath, "ship.bump"); v != "minor" {
				t.Errorf("user ship.bump = %v, want minor", v)
			}
		})
	}
}

// TestDashboardSetupSave_UserCallError checks that a Go error of the user
// file write call is the error of the save, and that no project write runs.
func TestDashboardSetupSave_UserCallError(t *testing.T) {
	root, userPath := saveRepo(t, "[jira]\ndefaultProject = \"X\"\n", "[ship]\nbump = \"patch\"\n", "")
	boom := errors.New("user write boom")
	calls := replaceSetupWrite(t, func(_ int, contentRoot, stateRoot string, in SetupWriteSectionsIn) (SetupWriteSectionsOut, error) {
		if in.Target == "user" {
			return SetupWriteSectionsOut{}, boom
		}
		return setupWriteSections(contentRoot, stateRoot, in)
	})
	before := saveSnapshot(saveConfigPath(root), saveLocalPath(root), userPath)
	out, err := DashboardSetupSave(root, DashboardSetupSaveIn{
		Changes: []DashboardSetupChange{saveSet("ship", "bump", "minor"), saveSet("jira", "defaultProject", "Y")},
		Targets: map[string]string{"ship": "user"},
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the user write error", err)
	}
	if out.Written != nil || out.Failed != nil || out.OK {
		t.Errorf("out = %+v, want the zero result", out)
	}
	if len(*calls) != 1 || (*calls)[0].Target != "user" {
		t.Errorf("write calls = %+v, want only the user call", *calls)
	}
	if got := saveSnapshot(saveConfigPath(root), saveLocalPath(root), userPath); fmt.Sprint(got) != fmt.Sprint(before) {
		t.Error("a file changed")
	}
}

// TestDashboardSetupSave_FirstWriteFails checks that an error of the first
// write call is the error of the save.
func TestDashboardSetupSave_FirstWriteFails(t *testing.T) {
	root, userPath := saveRepo(t, "[jira]\ndefaultProject = \"X\"\n", "", "")
	replaceSetupWrite(t, func(int, string, string, SetupWriteSectionsIn) (SetupWriteSectionsOut, error) {
		return SetupWriteSectionsOut{}, &mcpserver.InfraError{Msg: "write refused"}
	})
	before := saveSnapshot(saveConfigPath(root), userPath)
	_, err := DashboardSetupSave(root, DashboardSetupSaveIn{Changes: []DashboardSetupChange{saveSet("jira", "defaultProject", "Y")}})
	if err == nil || err.Error() != "write refused" {
		t.Fatalf("err = %v, want the write error", err)
	}
	if got := saveSnapshot(saveConfigPath(root), userPath); fmt.Sprint(got) != fmt.Sprint(before) {
		t.Error("a file changed")
	}
}

// TestDashboardSetupSave_SectionNotTable checks the refusal of a stored
// section that is not a table.
func TestDashboardSetupSave_SectionNotTable(t *testing.T) {
	root, _ := saveRepo(t, "", "ship = \"x\"\n", "")
	before := saveSnapshot(saveLocalPath(root))
	_, err := DashboardSetupSave(root, DashboardSetupSaveIn{
		Changes: []DashboardSetupChange{saveSet("ship", "bump", "minor")},
		Targets: map[string]string{"ship": "project"},
	})
	file := saveLocalPath(root)
	var de *mcpserver.DataError
	if !errors.As(err, &de) {
		t.Fatalf("err = %v, want a DataError", err)
	}
	if want := "dashboard setup save: ship in " + file + " is not a table."; de.Msg != want {
		t.Errorf("msg = %q, want %q", de.Msg, want)
	}
	if want := "Change ship in " + file + " to a table, or remove the key. Then save again. Nothing was written."; de.Suggestion != want {
		t.Errorf("suggestion = %q, want %q", de.Suggestion, want)
	}
	if got := saveSnapshot(saveLocalPath(root)); fmt.Sprint(got) != fmt.Sprint(before) {
		t.Error("the file changed")
	}
}

// TestDashboardSetupSave_NestedNotTable checks the refusal of a parent key of
// a dotted field that is not a table.
func TestDashboardSetupSave_NestedNotTable(t *testing.T) {
	root, _ := saveRepo(t, "[version]\ntag = \"x\"\n", "", "")
	before := saveSnapshot(saveConfigPath(root))
	_, err := DashboardSetupSave(root, DashboardSetupSaveIn{Changes: []DashboardSetupChange{saveSet("version", "tag.prefix", "r")}})
	file := saveConfigPath(root)
	var de *mcpserver.DataError
	if !errors.As(err, &de) {
		t.Fatalf("err = %v, want a DataError", err)
	}
	if want := "dashboard setup save: version.tag in " + file + " is not a table."; de.Msg != want {
		t.Errorf("msg = %q, want %q", de.Msg, want)
	}
	if want := "Change version.tag in " + file + " to a table, or remove the key. Then save again. Nothing was written."; de.Suggestion != want {
		t.Errorf("suggestion = %q, want %q", de.Suggestion, want)
	}
	if got := saveSnapshot(saveConfigPath(root)); fmt.Sprint(got) != fmt.Sprint(before) {
		t.Error("the file changed")
	}
}

// TestDashboardSetupSave_ResetLastKey checks each reader of the reader table
// on the empty table that a reset of the last key leaves.
func TestDashboardSetupSave_ResetLastKey(t *testing.T) {
	root, _ := saveRepo(t, "[version.tag]\nenabled = true\n\n[jira]\ndefaultProject = \"A\"\n", "[ship]\nbump = \"patch\"\n", "")
	saveOK(t, root, DashboardSetupSaveIn{
		Changes: []DashboardSetupChange{saveReset("jira", "defaultProject"), saveReset("ship", "bump")},
		Targets: map[string]string{"ship": "project"},
	})
	if v, ok := saveValue(t, saveConfigPath(root), "jira"); !ok || fmt.Sprint(v) != "map[]" {
		t.Errorf("config.toml jira = %v, want an empty table", v)
	}
	if v, ok := saveValue(t, saveLocalPath(root), "ship"); !ok || fmt.Sprint(v) != "map[]" {
		t.Errorf("local.toml ship = %v, want an empty table", v)
	}

	// config.Read: a section other than version with no value, no error.
	if _, err := config.Read(root); err != nil {
		t.Errorf("config.Read: %v", err)
	}
	// config.ReadSection: an empty map.
	if got, err := config.ReadSection(root, "jira"); err != nil || got == nil || len(got) != 0 {
		t.Errorf("config.ReadSection(jira) = %v, %v; want an empty map", got, err)
	}
	// config.ReadLocalLayers: an empty map for the section.
	if _, project, err := config.ReadLocalLayers(root); err != nil {
		t.Errorf("config.ReadLocalLayers: %v", err)
	} else if ship, ok := project["ship"].(map[string]any); !ok || len(ship) != 0 {
		t.Errorf("ReadLocalLayers project ship = %v, want an empty map", project["ship"])
	}
	// readGuardrailCheckFile: valid TOML.
	if _, err := readGuardrailCheckFile(saveConfigPath(root)); err != nil {
		t.Errorf("readGuardrailCheckFile: %v", err)
	}
	// setupWriteSections writes a new key into the empty table.
	if out, err := setupWriteSections(root, root, SetupWriteSectionsIn{SectionsJSON: `{"ship":{"bump":"major"}}`}); err != nil || !out.OK {
		t.Errorf("setupWriteSections into an empty table = %+v, %v", out, err)
	}
	if v, _ := saveValue(t, saveLocalPath(root), "ship.bump"); v != "major" {
		t.Errorf("ship.bump = %v, want major", v)
	}

	// An empty [version] table: config.Read gives the version error.
	vroot, _ := saveRepo(t, "[version]\nmethod = \"push\"\n", "", "")
	saveOK(t, vroot, DashboardSetupSaveIn{Changes: []DashboardSetupChange{saveReset("version", "method")}})
	if v, ok := saveValue(t, saveConfigPath(vroot), "version"); !ok || fmt.Sprint(v) != "map[]" {
		t.Errorf("config.toml version = %v, want an empty table", v)
	}
	_, err := config.Read(vroot)
	if want := "config: version section requires at least one of tag.enabled or versionFile.enabled to be true"; err == nil || err.Error() != want {
		t.Errorf("config.Read on an empty [version] = %v, want %q", err, want)
	}
	// The CI script generator: no secret name, no error and no warning.
	ci, err := scaffoldCIWith(vroot, scaffoldOptions{}, noGhExec)
	if err != nil || len(ci.Warnings) != 0 {
		t.Errorf("scaffoldCIWith on an empty [version] = warnings %v, err %v", ci.Warnings, err)
	}
	// A later save still works.
	saveOK(t, vroot, DashboardSetupSaveIn{Changes: []DashboardSetupChange{saveSet("version", "tag.enabled", true)}})
	if _, err := config.Read(vroot); err != nil {
		t.Errorf("config.Read after a later save: %v", err)
	}
}

// TestDashboardSetupSave_CIFiles checks that a save of the version section
// lists the CI files that the write code created.
func TestDashboardSetupSave_CIFiles(t *testing.T) {
	root, _ := saveRepo(t, "[version.tag]\nenabled = true\n", "", "")
	out := saveOK(t, root, DashboardSetupSaveIn{Changes: []DashboardSetupChange{saveSet("version", "tag.prefix", "r")}})
	if len(out.CIFiles) == 0 {
		t.Fatal("ciFiles is empty after a version save")
	}
	for _, p := range out.CIFiles {
		if !scaffoldFileExists(filepath.Join(root, p)) && !scaffoldFileExists(p) {
			t.Errorf("CI file %s does not exist", p)
		}
	}
	out = saveOK(t, root, DashboardSetupSaveIn{Changes: []DashboardSetupChange{saveSet("jira", "defaultProject", "A")}})
	if out.CIFiles == nil || len(out.CIFiles) != 0 {
		t.Errorf("ciFiles = %v after a jira save, want []", out.CIFiles)
	}
}

// TestSetupParity_ToolAndSave runs one state of the target file through
// setup_write_sections and through the save of the page, and compares the
// results as the parity table states.
func TestSetupParity_ToolAndSave(t *testing.T) {
	shipSave := DashboardSetupSaveIn{
		Changes: []DashboardSetupChange{saveSet("ship", "bump", "minor")},
		Targets: map[string]string{"ship": "project"},
	}
	shipTool := SetupWriteSectionsIn{SectionsJSON: `{"ship":{"bump":"minor"}}`}

	// run seeds two repos with the same state, then calls the tool in one
	// and the save in the other.
	run := func(t *testing.T, configTOML string, seed func(t *testing.T, root string), tool SetupWriteSectionsIn, save DashboardSetupSaveIn) (toolRoot, saveRoot string, toolOut SetupWriteSectionsOut, toolErr error, saveOut DashboardSetupSaveOut, saveErr error) {
		toolRoot, _ = saveRepo(t, configTOML, "", "")
		seed(t, toolRoot)
		toolOut, toolErr = setupWriteSections(toolRoot, toolRoot, tool)
		saveRoot, _ = saveRepo(t, configTOML, "", "")
		seed(t, saveRoot)
		saveOut, saveErr = DashboardSetupSave(saveRoot, save)
		return
	}
	sameError := func(t *testing.T, toolErr, saveErr error, toolRoot, saveRoot string) {
		t.Helper()
		if toolErr == nil || saveErr == nil {
			t.Fatalf("tool err = %v, save err = %v; want both", toolErr, saveErr)
		}
		tk, tm, _ := dashSetupSuggestion(toolErr)
		sk, sm, _ := dashSetupSuggestion(saveErr)
		// The save names itself as the caller; the rest of the text is the same.
		sm = strings.Replace(sm, "dashboard setup save:", "setup_write_sections:", 1)
		if tk != sk || strings.ReplaceAll(tm, toolRoot, "<root>") != strings.ReplaceAll(sm, saveRoot, "<root>") {
			t.Errorf("tool %s %q, save %s %q; want the same class and text", tk, tm, sk, sm)
		}
	}
	sameFile := func(t *testing.T, toolRoot, saveRoot string) {
		t.Helper()
		tb, err := os.ReadFile(saveLocalPath(toolRoot))
		if err != nil {
			t.Fatalf("read tool local.toml: %v", err)
		}
		sb, err := os.ReadFile(saveLocalPath(saveRoot))
		if err != nil {
			t.Fatalf("read save local.toml: %v", err)
		}
		if string(tb) != string(sb) {
			t.Errorf("local.toml differs:\n--- tool ---\n%s\n--- save ---\n%s", tb, sb)
		}
	}

	t.Run("absent", func(t *testing.T) {
		toolRoot, saveRoot, toolOut, toolErr, saveOut, saveErr := run(t, "", func(*testing.T, string) {}, shipTool, shipSave)
		if toolErr != nil || saveErr != nil || !toolOut.OK || !saveOut.OK {
			t.Fatalf("tool %+v %v, save %+v %v", toolOut, toolErr, saveOut, saveErr)
		}
		sameFile(t, toolRoot, saveRoot)
	})

	t.Run("directory at the path", func(t *testing.T) {
		toolRoot, saveRoot, _, toolErr, _, saveErr := run(t, "", func(t *testing.T, root string) {
			if err := os.MkdirAll(saveLocalPath(root), 0o755); err != nil {
				t.Fatal(err)
			}
		}, shipTool, shipSave)
		sameError(t, toolErr, saveErr, toolRoot, saveRoot)
		var infra *mcpserver.InfraError
		if !errors.As(saveErr, &infra) {
			t.Errorf("save err = %T, want an InfraError", saveErr)
		}
	})

	t.Run("symlink to a file", func(t *testing.T) {
		toolRoot, saveRoot, toolOut, toolErr, saveOut, saveErr := run(t, "", func(t *testing.T, root string) {
			target := filepath.Join(t.TempDir(), "real-local.toml")
			dashSetupWrite(t, target, "[ship]\nbump = \"patch\"\n")
			if err := os.Symlink(target, saveLocalPath(root)); err != nil {
				t.Fatal(err)
			}
		}, shipTool, shipSave)
		if toolErr != nil || saveErr != nil || toolOut.OK != saveOut.OK {
			t.Fatalf("tool %+v %v, save %+v %v", toolOut, toolErr, saveOut, saveErr)
		}
		sameFile(t, toolRoot, saveRoot)
		ti, err := os.Lstat(saveLocalPath(toolRoot))
		if err != nil {
			t.Fatalf("lstat tool local.toml: %v", err)
		}
		si, err := os.Lstat(saveLocalPath(saveRoot))
		if err != nil {
			t.Fatalf("lstat save local.toml: %v", err)
		}
		if ti.Mode().Type() != si.Mode().Type() {
			t.Errorf("file type: tool %v, save %v", ti.Mode().Type(), si.Mode().Type())
		}
	})

	t.Run("over the size limit", func(t *testing.T) {
		toolRoot, saveRoot, _, toolErr, _, saveErr := run(t, "", func(t *testing.T, root string) {
			writeSizedTOML(t, saveLocalPath(root), maxCheckedFileBytes+1)
		}, shipTool, shipSave)
		sameError(t, toolErr, saveErr, toolRoot, saveRoot)
		var data *mcpserver.DataError
		if !errors.As(saveErr, &data) || !strings.Contains(data.Msg, fmt.Sprint(maxCheckedFileBytes)) {
			t.Errorf("save err = %v, want a DataError with the limit", saveErr)
		}
	})

	t.Run("not valid TOML", func(t *testing.T) {
		toolRoot, saveRoot, _, toolErr, _, saveErr := run(t, "", func(t *testing.T, root string) {
			dashSetupWrite(t, saveLocalPath(root), "[ship\n")
		}, shipTool, shipSave)
		sameError(t, toolErr, saveErr, toolRoot, saveRoot)
		var data *mcpserver.DataError
		if !errors.As(saveErr, &data) {
			t.Errorf("save err = %T, want a DataError", saveErr)
		}
	})

	t.Run("file that the write code cannot edit in place", func(t *testing.T) {
		configTOML := "plan = { tasks = { contractShape = \"full\" } }\n"
		_, _, toolOut, toolErr, saveOut, saveErr := run(t, configTOML, func(*testing.T, string) {},
			SetupWriteSectionsIn{SectionsJSON: `{"plan.tasks":{"contractShape":"minimal"}}`},
			DashboardSetupSaveIn{Changes: []DashboardSetupChange{saveSet("plan-tasks", "contractShape", "minimal")}})
		if toolErr != nil || saveErr != nil {
			t.Fatalf("tool err %v, save err %v", toolErr, saveErr)
		}
		want := rewroteWarning("plan.tasks", ".sdlc-v2/config.toml")
		if !slices.Contains(toolOut.Warnings, want) || !slices.Contains(saveOut.Warnings, want) {
			t.Errorf("tool warnings %q, save warnings %q; want both to hold %q", toolOut.Warnings, saveOut.Warnings, want)
		}
	})

	t.Run("section key that is not a table", func(t *testing.T) {
		_, saveRoot, toolOut, toolErr, _, saveErr := run(t, "", func(t *testing.T, root string) {
			dashSetupWrite(t, saveLocalPath(root), "ship = \"x\"\n")
		}, shipTool, shipSave)
		if toolErr != nil || !toolOut.OK {
			t.Errorf("tool = %+v, %v; want a write", toolOut, toolErr)
		}
		var data *mcpserver.DataError
		if !errors.As(saveErr, &data) || !strings.Contains(data.Msg, "ship in "+saveLocalPath(saveRoot)+" is not a table") {
			t.Errorf("save err = %v, want the not-a-table DataError", saveErr)
		}
	})

	t.Run("parent key of a dotted field that is not a table", func(t *testing.T) {
		configTOML := "[version]\ntag = \"x\"\n"
		_, saveRoot, toolOut, toolErr, _, saveErr := run(t, configTOML, func(*testing.T, string) {},
			SetupWriteSectionsIn{SectionsJSON: `{"version":{"tag.prefix":"r"}}`},
			DashboardSetupSaveIn{Changes: []DashboardSetupChange{saveSet("version", "tag.prefix", "r")}})
		if toolErr != nil || !toolOut.OK {
			t.Errorf("tool = %+v, %v; want a write", toolOut, toolErr)
		}
		var data *mcpserver.DataError
		if !errors.As(saveErr, &data) || !strings.Contains(data.Msg, "version.tag in "+saveConfigPath(saveRoot)+" is not a table") {
			t.Errorf("save err = %v, want the not-a-table DataError", saveErr)
		}
	})
}

// TestDashboardSetupCreate covers each change of the Create table.
func TestDashboardSetupCreate(t *testing.T) {
	t.Run("no config", func(t *testing.T) {
		root, _ := dashSetupRoot(t)
		out, err := DashboardSetupCreate(root)
		if err != nil {
			t.Fatal(err)
		}
		if !out.OK || out.Errors == nil || len(out.Errors) != 0 {
			t.Fatalf("out = %+v, want ok with errors []", out)
		}
		for _, want := range []string{".sdlc-v2/.gitignore", ".gitignore", ".sdlc-v2/config.toml", ".sdlc-v2/local.toml"} {
			if !slices.Contains(out.Created, want) {
				t.Errorf("created = %v, want it to list %s", out.Created, want)
			}
		}
		if out.Changed == nil || len(out.Changed) != 0 {
			t.Errorf("changed = %v, want []", out.Changed)
		}
		if info, err := os.Stat(filepath.Join(root, ".sdlc-v2", "runs")); err != nil || !info.IsDir() {
			t.Errorf(".sdlc-v2/runs/ not created: %v", err)
		}
		configBytes, err := os.ReadFile(saveConfigPath(root))
		if err != nil {
			t.Fatalf("read config.toml: %v", err)
		}
		if string(configBytes) != configTemplate {
			t.Error("config.toml is not the template")
		}
		localBytes, err := os.ReadFile(saveLocalPath(root))
		if err != nil {
			t.Fatalf("read local.toml: %v", err)
		}
		if string(localBytes) != localTemplate {
			t.Error("local.toml is not the template")
		}
	})

	t.Run("existing gitignore and old local.json", func(t *testing.T) {
		root, _ := dashSetupRoot(t)
		dashSetupWrite(t, filepath.Join(root, ".gitignore"), "node_modules/\n")
		dashSetupWrite(t, filepath.Join(root, ".sdlc-v2", "local.json"), "{}")
		out, err := DashboardSetupCreate(root)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Contains(out.Changed, ".gitignore") {
			t.Errorf("changed = %v, want .gitignore", out.Changed)
		}
		if _, err := os.Stat(filepath.Join(root, ".sdlc-v2", "local.json.bak")); err != nil {
			t.Errorf("local.json.bak: %v", err)
		}
		if !slices.Contains(out.Changed, ".sdlc-v2/local.json → local.json.bak") {
			t.Errorf("changed = %v, want the rename listed", out.Changed)
		}
	})

	t.Run("old local.json with a backup", func(t *testing.T) {
		root, _ := dashSetupRoot(t)
		dashSetupWrite(t, filepath.Join(root, ".sdlc-v2", "local.json"), "{\"a\":1}")
		dashSetupWrite(t, filepath.Join(root, ".sdlc-v2", "local.json.bak"), "{\"b\":2}")
		out, err := DashboardSetupCreate(root)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range out.Changed {
			if strings.Contains(c, "local.json") {
				t.Errorf("changed = %v lists a rename", out.Changed)
			}
		}
		got := saveSnapshot(filepath.Join(root, ".sdlc-v2", "local.json"), filepath.Join(root, ".sdlc-v2", "local.json.bak"))
		if got[filepath.Join(root, ".sdlc-v2", "local.json")] != "{\"a\":1}" || got[filepath.Join(root, ".sdlc-v2", "local.json.bak")] != "{\"b\":2}" {
			t.Errorf("local.json files changed: %v", got)
		}
	})

	t.Run(".sdlc-v2 is a file", func(t *testing.T) {
		root, _ := dashSetupRoot(t)
		dashSetupWrite(t, filepath.Join(root, ".sdlc-v2"), "not a folder")
		_, err := DashboardSetupCreate(root)
		var infra *mcpserver.InfraError
		if !errors.As(err, &infra) {
			t.Fatalf("err = %v, want an InfraError", err)
		}
		if want := "dashboard setup: stat " + saveConfigPath(root) + ": "; !strings.HasPrefix(infra.Msg, want) {
			t.Errorf("msg = %q, want the prefix %q", infra.Msg, want)
		}
		if _, err := os.Stat(filepath.Join(root, ".gitignore")); !errors.Is(err, os.ErrNotExist) {
			t.Error(".gitignore was created")
		}
	})

	t.Run("write error", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("root ignores the folder mode")
		}
		root, _ := dashSetupRoot(t)
		if _, err := DashboardSetupCreate(root); err != nil {
			t.Fatal(err)
		}
		// Each file but config.toml exists, and config.toml cannot be written.
		if err := os.Remove(saveConfigPath(root)); err != nil {
			t.Fatal(err)
		}
		dataDir := filepath.Join(root, ".sdlc-v2")
		if err := os.Chmod(dataDir, 0o555); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(dataDir, 0o755) })
		out, err := DashboardSetupCreate(root)
		if err != nil {
			t.Fatal(err)
		}
		if out.OK {
			t.Error("ok = true, want false")
		}
		if !slices.ContainsFunc(out.Errors, func(e string) bool { return strings.HasPrefix(e, "config.toml: ") }) {
			t.Errorf("errors = %q, want a config.toml entry", out.Errors)
		}
		if out.Created == nil || len(out.Created) != 0 {
			t.Errorf("created = %#v, want []", out.Created)
		}
		if out.Changed == nil || len(out.Changed) != 0 {
			t.Errorf("changed = %#v, want []", out.Changed)
		}
	})

	for _, tc := range []struct {
		name, state string
		seed        func(t *testing.T, root string)
	}{
		{"ready", SetupStateReady, func(t *testing.T, root string) { dashSetupWrite(t, saveConfigPath(root), "") }},
		{"legacy-config", SetupStateLegacy, func(t *testing.T, root string) {
			dashSetupWrite(t, filepath.Join(root, ".sdlc-v2", "config.json"), "{}")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, _ := dashSetupRoot(t)
			tc.seed(t, root)
			_, err := DashboardSetupCreate(root)
			var nr *DashboardSetupNotReady
			if !errors.As(err, &nr) {
				t.Fatalf("err = %v, want *DashboardSetupNotReady", err)
			}
			if nr.State != tc.state || nr.Suggestion != "The repo already has a config. Reload the Setup tab." {
				t.Errorf("got %+v", nr)
			}
			if _, err := os.Stat(saveLocalPath(root)); !errors.Is(err, os.ErrNotExist) {
				t.Error("local.toml was created")
			}
		})
	}
}

// TestDashboardSetupSave_CollectFallbackMessage checks the message of a
// section that the write call did not write and gave no reason for.
func TestDashboardSetupSave_CollectFallbackMessage(t *testing.T) {
	root, _ := saveRepo(t, "[jira]\ndefaultProject = \"X\"\n", "", "")
	replaceSetupWrite(t, func(int, string, string, SetupWriteSectionsIn) (SetupWriteSectionsOut, error) {
		return SetupWriteSectionsOut{}, nil
	})
	out, err := DashboardSetupSave(root, DashboardSetupSaveIn{Changes: []DashboardSetupChange{saveSet("jira", "defaultProject", "Y")}})
	if err != nil {
		t.Fatal(err)
	}
	if out.OK {
		t.Error("ok = true, want false")
	}
	want := []DashboardSetupFailure{{Section: "jira", File: saveConfigPath(root), Message: "setup_write_sections did not write the section and gave no reason"}}
	if !slices.Equal(out.Failed, want) || len(out.Written) != 0 {
		t.Errorf("failed = %+v, written = %+v; want failed %+v", out.Failed, out.Written, want)
	}
}

// TestDashboardSetupSave_CollectSectionErrors checks that each failed section
// of one write call gets its own message from the result of that call.
func TestDashboardSetupSave_CollectSectionErrors(t *testing.T) {
	root, _ := saveRepo(t, "[jira]\ndefaultProject = \"X\"\n", "", "")
	replaceSetupWrite(t, func(int, string, string, SetupWriteSectionsIn) (SetupWriteSectionsOut, error) {
		return SetupWriteSectionsOut{
			Written:     []string{"ship"},
			Errors:      []string{"section jira: jira reason", "section plan.tasks: plan reason"},
			sectionErrs: map[string]string{"jira": "jira reason", "plan.tasks": "plan reason"},
		}, nil
	})
	out, err := DashboardSetupSave(root, DashboardSetupSaveIn{
		Changes: []DashboardSetupChange{
			saveSet("jira", "defaultProject", "Y"),
			saveSet("plan-tasks", "contractShape", "minimal"),
			saveSet("ship", "bump", "minor"),
		},
		Targets: map[string]string{"ship": "project"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.OK {
		t.Error("ok = true, want false")
	}
	if want := []DashboardSetupWrite{{Section: "ship", File: saveLocalPath(root)}}; !slices.Equal(out.Written, want) {
		t.Errorf("written = %+v, want %+v", out.Written, want)
	}
	wantFailed := []DashboardSetupFailure{
		{Section: "jira", File: saveConfigPath(root), Message: "jira reason"},
		{Section: "plan-tasks", File: saveConfigPath(root), Message: "plan reason"},
	}
	if !slices.Equal(out.Failed, wantFailed) {
		t.Errorf("failed = %+v, want %+v", out.Failed, wantFailed)
	}
}

// TestDashboardSetupSave_CollectWarningsAndNext checks that the save keeps
// the warnings of the user call and of the project call in call order, and
// joins the Next texts of both calls with a space.
func TestDashboardSetupSave_CollectWarningsAndNext(t *testing.T) {
	cases := []struct {
		name, userNext, projectNext, wantNext string
	}{
		{"both calls", "Edit the user file by hand.", "Edit config.toml by hand.", "Edit the user file by hand. Edit config.toml by hand."},
		{"project call only", "", "Edit config.toml by hand.", "Edit config.toml by hand."},
		{"user call only", "Edit the user file by hand.", "", "Edit the user file by hand."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root, userPath := saveRepo(t, "[jira]\ndefaultProject = \"X\"\n", "", "")
			calls := replaceSetupWrite(t, func(call int, _, _ string, in SetupWriteSectionsIn) (SetupWriteSectionsOut, error) {
				if in.Target == "user" {
					return SetupWriteSectionsOut{OK: true, Written: []string{"ship"}, Warnings: []string{"user warning 1", "user warning 2"}, Next: tc.userNext}, nil
				}
				return SetupWriteSectionsOut{OK: true, Written: []string{"jira"}, Warnings: []string{"project warning"}, Next: tc.projectNext}, nil
			})
			out := saveOK(t, root, DashboardSetupSaveIn{
				Changes: []DashboardSetupChange{saveSet("ship", "bump", "minor"), saveSet("jira", "defaultProject", "Y")},
				Targets: map[string]string{"ship": "user"},
			})
			if len(*calls) != 2 || (*calls)[0].Target != "user" || (*calls)[1].Target != "" {
				t.Fatalf("write calls = %+v, want the user call, then the project call", *calls)
			}
			if want := []string{"user warning 1", "user warning 2", "project warning"}; !slices.Equal(out.Warnings, want) {
				t.Errorf("warnings = %q, want %q", out.Warnings, want)
			}
			if out.Next != tc.wantNext {
				t.Errorf("next = %q, want %q", out.Next, tc.wantNext)
			}
			want := []DashboardSetupWrite{{Section: "ship", File: userPath}, {Section: "jira", File: saveConfigPath(root)}}
			if !slices.Equal(out.Written, want) {
				t.Errorf("written = %+v, want %+v", out.Written, want)
			}
		})
	}
}

// TestDashboardSetupSave_ShipDefaultTarget checks that a save of the ship
// section with no targets entry uses its default target: the project file.
func TestDashboardSetupSave_ShipDefaultTarget(t *testing.T) {
	root, userPath := saveRepo(t, "", "[ship]\nauto = true\n", "")
	out := saveOK(t, root, DashboardSetupSaveIn{Changes: []DashboardSetupChange{saveSet("ship", "bump", "minor")}})
	if want := []DashboardSetupWrite{{Section: "ship", File: saveLocalPath(root)}}; !slices.Equal(out.Written, want) {
		t.Errorf("written = %+v, want %+v", out.Written, want)
	}
	if v, _ := saveValue(t, saveLocalPath(root), "ship.bump"); v != "minor" {
		t.Errorf("project ship.bump = %v, want minor", v)
	}
	if v, _ := saveValue(t, saveLocalPath(root), "ship.auto"); v != true {
		t.Errorf("project ship.auto = %v, want it kept", v)
	}
	if _, err := os.Stat(userPath); !errors.Is(err, os.ErrNotExist) {
		t.Error("the user file was created")
	}
}

// TestDashboardSetupSave_MixedTargets checks one save with three local
// sections and different targets: each section goes to its own file.
func TestDashboardSetupSave_MixedTargets(t *testing.T) {
	root, userPath := saveRepo(t, "", "", "")
	out := saveOK(t, root, DashboardSetupSaveIn{
		Changes: []DashboardSetupChange{
			saveSet("ship", "bump", "minor"),
			saveSet("review", "scope", "staged"),
			saveSet("automation", "mode", "unattended"),
		},
		// ship defaults to project and review to user: both are overridden.
		// automation has no entry, so it uses its default, user.
		Targets: map[string]string{"ship": "user", "review": "project"},
	})
	want := []DashboardSetupWrite{
		{Section: "automation", File: userPath},
		{Section: "review", File: saveLocalPath(root)},
		{Section: "ship", File: userPath},
	}
	slices.SortFunc(out.Written, func(a, b DashboardSetupWrite) int { return strings.Compare(a.Section, b.Section) })
	if !slices.Equal(out.Written, want) {
		t.Errorf("written = %+v, want %+v", out.Written, want)
	}
	checks := []struct{ path, dotted, want string }{
		{userPath, "ship.bump", "minor"},
		{saveLocalPath(root), "review.scope", "staged"},
		{userPath, "automation.mode", "unattended"},
	}
	for _, c := range checks {
		if v, _ := saveValue(t, c.path, c.dotted); v != c.want {
			t.Errorf("%s %s = %v, want %s", c.path, c.dotted, v, c.want)
		}
	}
	if _, ok := saveValue(t, saveLocalPath(root), "ship"); ok {
		t.Error("ship was written to the project file")
	}
	if _, ok := saveValue(t, userPath, "review"); ok {
		t.Error("review was written to the user file")
	}
}

// TestDashboardSetupSave_UserTargetProjectFileErrors checks the errors of the
// project file read of a section with the target user. The save writes no
// file.
func TestDashboardSetupSave_UserTargetProjectFileErrors(t *testing.T) {
	cases := []struct {
		name    string
		seed    func(t *testing.T, root string)
		change  DashboardSetupChange
		section string
		kind    string
		msg     func(root string) string // exact Msg; nil checks the prefix only
	}{
		{"directory at the path", func(t *testing.T, root string) {
			if err := os.MkdirAll(saveLocalPath(root), 0o755); err != nil {
				t.Fatal(err)
			}
		}, saveSet("ship", "bump", "minor"), "ship", "infra", nil},
		{"over the size limit", func(t *testing.T, root string) {
			writeSizedTOML(t, saveLocalPath(root), maxCheckedFileBytes+1)
		}, saveSet("ship", "bump", "minor"), "ship", "data", nil},
		{"not valid TOML", func(t *testing.T, root string) {
			dashSetupWrite(t, saveLocalPath(root), "[ship\n")
		}, saveSet("ship", "bump", "minor"), "ship", "data", nil},
		{"section not a table", func(t *testing.T, root string) {
			dashSetupWrite(t, saveLocalPath(root), "ship = \"x\"\n")
		}, saveSet("ship", "bump", "minor"), "ship", "data", func(root string) string {
			return "dashboard setup save: ship in " + saveLocalPath(root) + " is not a table."
		}},
		{"parent of a dotted field not a table", func(t *testing.T, root string) {
			dashSetupWrite(t, saveLocalPath(root), "[automation]\ndrift = \"x\"\n")
		}, saveSet("automation", "drift.maxErrorRate", float64(0.5)), "automation", "data", func(root string) string {
			return "dashboard setup save: automation.drift in " + saveLocalPath(root) + " is not a table."
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root, userPath := saveRepo(t, "", "", "")
			tc.seed(t, root)
			before := saveSnapshot(saveConfigPath(root), userPath)
			_, err := DashboardSetupSave(root, DashboardSetupSaveIn{
				Changes: []DashboardSetupChange{tc.change},
				Targets: map[string]string{tc.section: "user"},
			})
			kind, msg, suggestion := dashSetupSuggestion(err)
			if kind != tc.kind {
				t.Fatalf("err = %T %v, want a %s error", err, err, tc.kind)
			}
			if !strings.HasPrefix(msg, "dashboard setup save:") || suggestion == "" {
				t.Errorf("msg = %q, suggestion = %q; want the save prefix and a suggestion", msg, suggestion)
			}
			if tc.msg != nil && msg != tc.msg(root) {
				t.Errorf("msg = %q, want %q", msg, tc.msg(root))
			}
			if got := saveSnapshot(saveConfigPath(root), userPath); fmt.Sprint(got) != fmt.Sprint(before) {
				t.Error("a file changed")
			}
		})
	}
}

// TestDashboardSetupSave_RateFields checks the rate fields of the automation
// section: a decimal text is stored as a number, a value that is not a number
// from 0 to 1 is refused, and minErrorFloor accepts 0.
func TestDashboardSetupSave_RateFields(t *testing.T) {
	t.Run("decimal text stored as a number", func(t *testing.T) {
		root, _ := saveRepo(t, "", "", "")
		saveOK(t, root, DashboardSetupSaveIn{
			Changes: []DashboardSetupChange{
				saveSet("automation", "drift.maxErrorRate", "0.3"),
				saveSet("automation", "drift.minErrorFloor", float64(0)),
			},
			Targets: map[string]string{"automation": "project"},
		})
		v, _ := saveValue(t, saveLocalPath(root), "automation.drift.maxErrorRate")
		if n, ok := v.(float64); !ok || n != 0.3 {
			t.Errorf("automation.drift.maxErrorRate = %#v, want the number 0.3", v)
		}
		if v, ok := saveValue(t, saveLocalPath(root), "automation.drift.minErrorFloor"); !ok || fmt.Sprint(v) != "0" {
			t.Errorf("automation.drift.minErrorFloor = %#v, want 0", v)
		}
	})

	t.Run("minErrorFloor bounds", func(t *testing.T) {
		for _, sec := range saveFormSections() {
			for _, f := range sec.Fields {
				if sec.ID == "automation" && f.Name == "drift.minErrorFloor" {
					if f.Min == nil || *f.Min != 0 || f.Max != nil {
						t.Errorf("minErrorFloor min = %v, max = %v; want min 0 and no max", f.Min, f.Max)
					}
					return
				}
			}
		}
		t.Error("no field automation drift.minErrorFloor")
	})

	cases := []struct {
		name    string
		changes []DashboardSetupChange
		want    string
	}{
		{"one refused value", []DashboardSetupChange{saveSet("automation", "drift.maxErrorRate", "banana")},
			`dashboard setup save: 1 field value is refused. automation.drift.maxErrorRate: a number from 0 to 1 is required, got "banana".`},
		{"two refused values", []DashboardSetupChange{
			saveSet("automation", "drift.maxWarningRate", float64(1.5)),
			saveSet("automation", "drift.maxErrorRate", "banana"),
		}, `dashboard setup save: 2 field values are refused. automation.drift.maxErrorRate: a number from 0 to 1 is required, got "banana". automation.drift.maxWarningRate: a number from 0 to 1 is required, got 1.5.`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root, userPath := saveRepo(t, "", "", "")
			before := saveSnapshot(saveLocalPath(root), userPath)
			_, err := DashboardSetupSave(root, DashboardSetupSaveIn{Changes: tc.changes, Targets: map[string]string{"automation": "project"}})
			var invalid *DashboardSetupInvalid
			if !errors.As(err, &invalid) {
				t.Fatalf("err = %v, want *DashboardSetupInvalid", err)
			}
			if err.Error() != tc.want {
				t.Errorf("Error() = %q, want %q", err.Error(), tc.want)
			}
			if got := saveSnapshot(saveLocalPath(root), userPath); fmt.Sprint(got) != fmt.Sprint(before) {
				t.Error("a refused save changed a file")
			}
		})
	}
}

// TestSetupApplyChange_Edges covers the parent arms of setupApplyChange.
func TestSetupApplyChange_Edges(t *testing.T) {
	t.Run("reset with an absent parent", func(t *testing.T) {
		table := map[string]any{"a": "1"}
		bad, removed := setupApplyChange(table, "x.y.z", setupOpReset, nil)
		if bad != "" || removed {
			t.Errorf("got bad %q, removed %v; want no change", bad, removed)
		}
		if fmt.Sprint(table) != "map[a:1]" {
			t.Errorf("table = %v, want it unchanged", table)
		}
	})

	t.Run("set creates each absent parent", func(t *testing.T) {
		table := map[string]any{"keep": true}
		bad, removed := setupApplyChange(table, "a.b.c", setupOpSet, "v")
		if bad != "" || removed {
			t.Errorf("got bad %q, removed %v", bad, removed)
		}
		if got, ok := setupLookup(table, []string{"a", "b", "c"}); !ok || got != "v" {
			t.Errorf("a.b.c = %v, want v; table = %v", got, table)
		}
		if table["keep"] != true {
			t.Errorf("table = %v, want keep kept", table)
		}
	})

	t.Run("set below a present parent", func(t *testing.T) {
		table := map[string]any{"a": map[string]any{"old": "1"}}
		setupApplyChange(table, "a.b.c", setupOpSet, "v")
		if fmt.Sprint(table) != "map[a:map[b:map[c:v] old:1]]" {
			t.Errorf("table = %v", table)
		}
	})

	t.Run("reset of an absent leaf", func(t *testing.T) {
		table := map[string]any{"a": map[string]any{"b": "1"}}
		bad, removed := setupApplyChange(table, "a.c", setupOpReset, nil)
		if bad != "" || removed {
			t.Errorf("got bad %q, removed %v; want no change", bad, removed)
		}
	})

	t.Run("deep parent not a table", func(t *testing.T) {
		table := map[string]any{"a": map[string]any{"b": "scalar"}}
		bad, _ := setupApplyChange(table, "a.b.c", setupOpSet, "v")
		if bad != "a.b" {
			t.Errorf("bad = %q, want a.b", bad)
		}
		if fmt.Sprint(table) != "map[a:map[b:scalar]]" {
			t.Errorf("table = %v, want it unchanged", table)
		}
	})
}

// TestSetupDeepCopy_Lists checks that a copy of a list shares no table or
// list with the original.
func TestSetupDeepCopy_Lists(t *testing.T) {
	t.Run("[]any", func(t *testing.T) {
		orig := []any{map[string]any{"k": "v"}, []any{"x"}, "s"}
		cp := setupDeepCopy(orig).([]any)
		cp[0].(map[string]any)["k"] = "changed"
		cp[1].([]any)[0] = "changed"
		cp[2] = "changed"
		if fmt.Sprint(orig) != "[map[k:v] [x] s]" {
			t.Errorf("original = %v, want it unchanged", orig)
		}
	})

	t.Run("[]map[string]any", func(t *testing.T) {
		orig := []map[string]any{{"k": "v", "nested": map[string]any{"n": "1"}}}
		cp := setupDeepCopy(orig).([]map[string]any)
		cp[0]["k"] = "changed"
		cp[0]["nested"].(map[string]any)["n"] = "changed"
		if fmt.Sprint(orig) != "[map[k:v nested:map[n:1]]]" {
			t.Errorf("original = %v, want it unchanged", orig)
		}
	})

	t.Run("table holding lists", func(t *testing.T) {
		orig := map[string]any{"list": []any{"a"}, "rows": []map[string]any{{"r": "1"}}}
		cp := setupCopyTable(orig)
		cp["list"].([]any)[0] = "changed"
		cp["rows"].([]map[string]any)[0]["r"] = "changed"
		if fmt.Sprint(orig) != "map[list:[a] rows:[map[r:1]]]" {
			t.Errorf("original = %v, want it unchanged", orig)
		}
	})
}

// saveGuardrailConfig is a config.toml with two plan guardrails and one
// execute guardrail, in the named-table form.
const saveGuardrailConfig = `# Team guardrails. Keep ids in kebab-case.
[plan.guardrails.dry]
description = "Keep code DRY."
severity = "error"

[plan.guardrails.old-rule]
description = "Old rule."
severity = "error"

[execute.guardrails.no-skip]
description = "Do not skip tests."
severity = "warning"
`

func saveGrAdd(id, severity, description string) DashboardSetupGuardrailChange {
	return DashboardSetupGuardrailChange{ID: id, Op: "add", Severity: severity, Description: description}
}

func saveGrChange(id, severity, description string) DashboardSetupGuardrailChange {
	return DashboardSetupGuardrailChange{ID: id, Op: "change", Severity: severity, Description: description}
}

func saveGrRemove(id string) DashboardSetupGuardrailChange {
	return DashboardSetupGuardrailChange{ID: id, Op: "remove"}
}

// saveGrIn is a guardrail-only save request with the changes of one section.
func saveGrIn(section string, changes ...DashboardSetupGuardrailChange) DashboardSetupSaveIn {
	return DashboardSetupSaveIn{Guardrails: map[string][]DashboardSetupGuardrailChange{section: changes}}
}

// saveGrAdds returns n adds with distinct valid ids.
func saveGrAdds(n int) []DashboardSetupGuardrailChange {
	out := make([]DashboardSetupGuardrailChange, n)
	for i := range out {
		out[i] = saveGrAdd(fmt.Sprintf("rule-%d", i), "error", "A rule.")
	}
	return out
}

// TestDashboardSetupSave_GuardrailRequest covers each guardrail request rule.
// Each case returns a DomainError with the exact Msg and Suggestion of the
// rule, and no file changes.
func TestDashboardSetupSave_GuardrailRequest(t *testing.T) {
	limitSuggestion := fmt.Sprintf("Save at most %d guardrail changes of one list at one time.", maxGuardrailChangesPerList)
	idSuggestion := fmt.Sprintf("Use an id of at most %d bytes.", guardrailIDMaxBytes)
	needsSuggestion := "Send both fields. change replaces both stored values."
	removeSuggestion := "Send only id and op for a remove."
	cases := []struct {
		name, rule, suggestion string
		in                     DashboardSetupSaveIn
	}{
		{"unknown section", "guardrails has unknown section commit", "Use plan-guardrails or execution-guardrails.",
			saveGrIn("commit", saveGrRemove("dry"))},
		{"empty list", "guardrails.plan-guardrails is empty", "Omit a section with no change.",
			DashboardSetupSaveIn{Guardrails: map[string][]DashboardSetupGuardrailChange{"plan-guardrails": {}}}},
		{"over the change limit", fmt.Sprintf("guardrails.plan-guardrails has %d changes, over the limit of %d", maxGuardrailChangesPerList+1, maxGuardrailChangesPerList), limitSuggestion,
			saveGrIn("plan-guardrails", saveGrAdds(maxGuardrailChangesPerList+1)...)},
		{"one id twice", "guardrails.plan-guardrails changes id dry twice", "Send one change for each id.",
			saveGrIn("plan-guardrails", saveGrRemove("dry"), saveGrAdd("dry", "error", "Keep code DRY."))},
		{"id over the limit", fmt.Sprintf("guardrails.execution-guardrails has an id of %d bytes, over the limit of %d", guardrailIDMaxBytes+1, guardrailIDMaxBytes), idSuggestion,
			saveGrIn("execution-guardrails", saveGrAdd(strings.Repeat("a", guardrailIDMaxBytes+1), "error", "A rule."))},
		{"bad op", "guardrails.plan-guardrails: op delete is not add, change or remove", "Use add, change or remove.",
			saveGrIn("plan-guardrails", DashboardSetupGuardrailChange{ID: "dry", Op: "delete"})},
		{"add with no severity", "guardrails.plan-guardrails: add of new-rule needs severity and description", needsSuggestion,
			saveGrIn("plan-guardrails", saveGrAdd("new-rule", "", "A rule."))},
		{"add with a 0-byte description", "guardrails.plan-guardrails: add of new-rule needs severity and description", needsSuggestion,
			saveGrIn("plan-guardrails", saveGrAdd("new-rule", "error", ""))},
		{"change with no severity", "guardrails.plan-guardrails: change of dry needs severity and description", needsSuggestion,
			saveGrIn("plan-guardrails", saveGrChange("dry", "", "Keep code DRY."))},
		{"change with no description", "guardrails.plan-guardrails: change of dry needs severity and description", needsSuggestion,
			saveGrIn("plan-guardrails", saveGrChange("dry", "warning", ""))},
		{"remove with a severity", "guardrails.plan-guardrails: remove of dry takes no severity or description", removeSuggestion,
			saveGrIn("plan-guardrails", DashboardSetupGuardrailChange{ID: "dry", Op: "remove", Severity: "error"})},
		{"remove with a description", "guardrails.plan-guardrails: remove of dry takes no severity or description", removeSuggestion,
			saveGrIn("plan-guardrails", DashboardSetupGuardrailChange{ID: "dry", Op: "remove", Description: "x"})},
		// Two broken sections: the keys are checked in sorted order.
		{"two empty lists", "guardrails.execution-guardrails is empty", "Omit a section with no change.",
			DashboardSetupSaveIn{Guardrails: map[string][]DashboardSetupGuardrailChange{"plan-guardrails": {}, "execution-guardrails": {}}}},
		// A form change does not hide a broken guardrail change.
		{"form change and a bad guardrail change", "guardrails.plan-guardrails: op delete is not add, change or remove", "Use add, change or remove.",
			DashboardSetupSaveIn{
				Changes:    []DashboardSetupChange{saveSet("jira", "defaultProject", "A")},
				Guardrails: map[string][]DashboardSetupGuardrailChange{"plan-guardrails": {{ID: "dry", Op: "delete"}}},
			}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root, userPath := saveRepo(t, saveGuardrailConfig, "", "")
			before := saveSnapshot(saveConfigPath(root), saveLocalPath(root), userPath)
			_, err := DashboardSetupSave(root, tc.in)
			var de *mcpserver.DomainError
			if !errors.As(err, &de) {
				t.Fatalf("err = %v, want a DomainError", err)
			}
			if want := saveRequestMsg(tc.rule); de.Msg != want {
				t.Errorf("msg = %q, want %q", de.Msg, want)
			}
			if de.Suggestion != tc.suggestion {
				t.Errorf("suggestion = %q, want %q", de.Suggestion, tc.suggestion)
			}
			if got := saveSnapshot(saveConfigPath(root), saveLocalPath(root), userPath); fmt.Sprint(got) != fmt.Sprint(before) {
				t.Error("a refused request changed a file")
			}
		})
	}
}

// TestDashboardSetupSave_GuardrailLimits checks that each limit takes its
// largest value: 100 changes of one list, a description of 1024 bytes, and
// an id of 64 bytes. The values one over each limit are refused, and each
// refusal names the limit.
func TestDashboardSetupSave_GuardrailLimits(t *testing.T) {
	check := func(t *testing.T, in DashboardSetupSaveIn) (setupGuardrailWrites, error) {
		t.Helper()
		root, _ := saveRepo(t, saveGuardrailConfig, "", "")
		return checkSetupGuardrails(root, "", in, setupDocCache{})
	}
	t.Run("100 changes", func(t *testing.T) {
		gr, err := check(t, saveGrIn("plan-guardrails", saveGrAdds(maxGuardrailChangesPerList)...))
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if len(gr) != maxGuardrailChangesPerList {
			t.Errorf("writes = %d, want %d", len(gr), maxGuardrailChangesPerList)
		}
	})
	t.Run("101 changes", func(t *testing.T) {
		_, err := check(t, saveGrIn("plan-guardrails", saveGrAdds(maxGuardrailChangesPerList+1)...))
		if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("over the limit of %d", maxGuardrailChangesPerList)) {
			t.Errorf("err = %v, want a refusal that names the limit %d", err, maxGuardrailChangesPerList)
		}
	})
	t.Run("64-byte id", func(t *testing.T) {
		id := strings.Repeat("a", guardrailIDMaxBytes)
		gr, err := check(t, saveGrIn("plan-guardrails", saveGrAdd(id, "error", "A rule.")))
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if _, ok := gr["plan.guardrails."+id]; !ok {
			t.Errorf("writes = %v, want the key of the 64-byte id", gr)
		}
	})
	t.Run("65-byte id", func(t *testing.T) {
		_, err := check(t, saveGrIn("plan-guardrails", saveGrAdd(strings.Repeat("a", guardrailIDMaxBytes+1), "error", "A rule.")))
		if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("over the limit of %d", guardrailIDMaxBytes)) {
			t.Errorf("err = %v, want a refusal that names the limit %d", err, guardrailIDMaxBytes)
		}
	})
	t.Run("1024-byte description", func(t *testing.T) {
		if _, err := check(t, saveGrIn("plan-guardrails", saveGrAdd("long", "error", strings.Repeat("a", guardrailDescMaxBytes)))); err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
	})
	t.Run("1025-byte description", func(t *testing.T) {
		_, err := check(t, saveGrIn("plan-guardrails", saveGrAdd("long", "error", strings.Repeat("a", guardrailDescMaxBytes+1))))
		var invalid *DashboardSetupInvalid
		if !errors.As(err, &invalid) {
			t.Fatalf("err = %v, want *DashboardSetupInvalid", err)
		}
		if len(invalid.Fields) != 1 || !strings.Contains(invalid.Fields[0].Message, fmt.Sprintf("exceeds %d bytes", guardrailDescMaxBytes)) {
			t.Errorf("fields = %+v, want one entry that names the limit %d", invalid.Fields, guardrailDescMaxBytes)
		}
	})
	t.Run("0-byte description", func(t *testing.T) {
		// A 0-byte description is an absent field: a request rule refuses it.
		_, err := check(t, saveGrIn("plan-guardrails", saveGrAdd("empty", "error", "")))
		var de *mcpserver.DomainError
		if !errors.As(err, &de) || !strings.Contains(de.Msg, "needs severity and description") {
			t.Errorf("err = %v, want the request rule for a missing description", err)
		}
	})
}

// TestDashboardSetupSave_GuardrailWrites checks the write keys of a valid
// save: add and change give the description and the severity, remove gives
// nil, and a change from error to warning is accepted.
func TestDashboardSetupSave_GuardrailWrites(t *testing.T) {
	root, _ := saveRepo(t, saveGuardrailConfig, "", "")
	in := DashboardSetupSaveIn{Guardrails: map[string][]DashboardSetupGuardrailChange{
		"plan-guardrails": {
			saveGrChange("dry", "warning", "Keep code DRY."),
			saveGrAdd("new-rule", "error", "New rule."),
			saveGrRemove("old-rule"),
		},
		"execution-guardrails": {saveGrRemove("no-skip")},
	}}
	gr, err := checkSetupGuardrails(root, "", in, setupDocCache{})
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	want := setupGuardrailWrites{
		"plan.guardrails.dry":        {section: "plan-guardrails", id: "dry", value: map[string]any{"description": "Keep code DRY.", "severity": "warning"}},
		"plan.guardrails.new-rule":   {section: "plan-guardrails", id: "new-rule", value: map[string]any{"description": "New rule.", "severity": "error"}},
		"plan.guardrails.old-rule":   {section: "plan-guardrails", id: "old-rule"},
		"execute.guardrails.no-skip": {section: "execution-guardrails", id: "no-skip"},
	}
	if !reflect.DeepEqual(gr, want) {
		t.Errorf("writes = %+v, want %+v", gr, want)
	}

	t.Run("no guardrail change reads no file", func(t *testing.T) {
		root, _ := dashSetupRoot(t)
		if err := os.MkdirAll(saveConfigPath(root), 0o755); err != nil {
			t.Fatal(err)
		}
		gr, err := checkSetupGuardrails(root, "", DashboardSetupSaveIn{Changes: []DashboardSetupChange{saveSet("jira", "defaultProject", "A")}}, setupDocCache{})
		if err != nil || gr == nil || len(gr) != 0 {
			t.Errorf("got %v, %v, want an empty map and no error", gr, err)
		}
	})
}

// TestDashboardSetupSave_GuardrailStale checks that a stale change refuses
// the save with SETUP_INVALID and one field entry for each stale id, sorted by
// path, and that no file changes.
func TestDashboardSetupSave_GuardrailStale(t *testing.T) {
	root, userPath := saveRepo(t, saveGuardrailConfig, "", "")
	before := saveSnapshot(saveConfigPath(root), saveLocalPath(root), userPath)
	_, err := DashboardSetupSave(root, DashboardSetupSaveIn{Guardrails: map[string][]DashboardSetupGuardrailChange{
		"plan-guardrails": {
			saveGrAdd("dry", "error", "Keep code DRY."),
			saveGrChange("gone", "error", "Gone."),
			saveGrRemove("also-gone"),
			saveGrChange("old-rule", "warning", "Old rule."), // not stale
		},
		"execution-guardrails": {saveGrAdd("no-skip", "error", "Do not skip tests.")},
	}})
	var invalid *DashboardSetupInvalid
	if !errors.As(err, &invalid) {
		t.Fatalf("err = %v, want *DashboardSetupInvalid", err)
	}
	var got []string
	for _, f := range invalid.Fields {
		got = append(got, f.Section+" "+f.Field+" "+f.Path)
		if !strings.HasSuffix(f.Message, "Reload the page.") {
			t.Errorf("message %q does not end with the reload step", f.Message)
		}
	}
	want := []string{
		"execution-guardrails no-skip execute.guardrails.no-skip",
		"plan-guardrails also-gone plan.guardrails.also-gone",
		"plan-guardrails dry plan.guardrails.dry",
		"plan-guardrails gone plan.guardrails.gone",
	}
	if !slices.Equal(got, want) {
		t.Errorf("fields = %v, want %v", got, want)
	}
	if got := saveSnapshot(saveConfigPath(root), saveLocalPath(root), userPath); fmt.Sprint(got) != fmt.Sprint(before) {
		t.Error("a refused save changed a file")
	}
}

// TestDashboardSetupSave_GuardrailRules checks that a merged list that
// breaks a per-entry guardrail rule refuses the save with SETUP_INVALID, and
// that no file changes. The merged list holds the entries of the file too.
func TestDashboardSetupSave_GuardrailRules(t *testing.T) {
	configTOML := saveGuardrailConfig + "\n[plan.guardrails.no-desc]\nseverity = \"error\"\n"
	root, userPath := saveRepo(t, configTOML, "", "")
	before := saveSnapshot(saveConfigPath(root), saveLocalPath(root), userPath)
	_, err := DashboardSetupSave(root, saveGrIn("plan-guardrails",
		saveGrAdd("Bad_ID", "error", "A rule."),
		saveGrAdd("blank", "error", "   "),
		saveGrChange("dry", "fatal", "Keep code DRY."),
	))
	var invalid *DashboardSetupInvalid
	if !errors.As(err, &invalid) {
		t.Fatalf("err = %v, want *DashboardSetupInvalid", err)
	}
	var got []string
	for _, f := range invalid.Fields {
		got = append(got, f.Field+": "+f.Message)
	}
	wantParts := []string{"Bad_ID: Bad_ID: id must match kebab-case", "blank: blank: description cannot be empty", "dry: dry: severity must be", "no-desc: no-desc: description is missing"}
	if len(got) != len(wantParts) {
		t.Fatalf("fields = %v, want %d entries", got, len(wantParts))
	}
	for i, w := range wantParts {
		if !strings.HasPrefix(got[i], w) {
			t.Errorf("field %d = %q, want the prefix %q", i, got[i], w)
		}
	}
	if got := saveSnapshot(saveConfigPath(root), saveLocalPath(root), userPath); fmt.Sprint(got) != fmt.Sprint(before) {
		t.Error("a refused save changed a file")
	}
}

// TestDashboardSetupSave_GuardrailInputStates checks the outcome of each
// state of config.toml. The file-level states give the same outcome for a
// form save and for a guardrail save of each list. The table-level states
// apply to a guardrail save of each list.
func TestDashboardSetupSave_GuardrailInputStates(t *testing.T) {
	type list struct{ section, path, parent string }
	lists := []list{{"plan-guardrails", "plan.guardrails", "plan"}, {"execution-guardrails", "execute.guardrails", "execute"}}
	saves := map[string]DashboardSetupSaveIn{
		"form save": {Changes: []DashboardSetupChange{saveSet("jira", "defaultProject", "A")}},
	}
	for _, l := range lists {
		saves[l.section+" save"] = saveGrIn(l.section, saveGrAdd("new-rule", "error", "New rule."))
	}

	fileStates := []struct {
		name  string
		seed  func(t *testing.T, root string)
		check func(t *testing.T, err error)
	}{
		{"file missing", func(*testing.T, string) {}, func(t *testing.T, err error) {
			var nr *DashboardSetupNotReady
			if !errors.As(err, &nr) || nr.State != SetupStateNoConfig {
				t.Errorf("err = %v, want *DashboardSetupNotReady %s", err, SetupStateNoConfig)
			}
		}},
		{"file over the size limit", func(t *testing.T, root string) {
			dashSetupWrite(t, saveConfigPath(root), "#"+strings.Repeat("x", maxCheckedFileBytes)+"\n")
		}, func(t *testing.T, err error) {
			var de *mcpserver.DataError
			if !errors.As(err, &de) || !strings.Contains(de.Msg, "byte limit") || de.Suggestion == "" {
				t.Errorf("err = %v, want a DataError over the byte limit with a Suggestion", err)
			}
		}},
		{"bad TOML", func(t *testing.T, root string) {
			dashSetupWrite(t, saveConfigPath(root), "[plan\n")
		}, func(t *testing.T, err error) {
			var de *mcpserver.DataError
			if !errors.As(err, &de) || !strings.Contains(de.Msg, "is not valid TOML") || de.Suggestion == "" {
				t.Errorf("err = %v, want a DataError for bad TOML with a Suggestion", err)
			}
		}},
		{"read failure", func(t *testing.T, root string) {
			// A directory in place of the file: the stat succeeds and the
			// read fails.
			if err := os.MkdirAll(saveConfigPath(root), 0o755); err != nil {
				t.Fatal(err)
			}
		}, func(t *testing.T, err error) {
			var ie *mcpserver.InfraError
			if !errors.As(err, &ie) || !strings.Contains(ie.Msg, "is a directory") {
				t.Errorf("err = %v, want an InfraError that names the read error", err)
			}
		}},
	}
	for _, fs := range fileStates {
		for name, in := range saves {
			t.Run(fs.name+"/"+name, func(t *testing.T) {
				root, _ := dashSetupRoot(t)
				fs.seed(t, root)
				before := saveSnapshot(saveConfigPath(root), saveLocalPath(root))
				_, err := DashboardSetupSave(root, in)
				fs.check(t, err)
				if got := saveSnapshot(saveConfigPath(root), saveLocalPath(root)); fmt.Sprint(got) != fmt.Sprint(before) {
					t.Error("a refused save changed a file")
				}
			})
		}
	}

	for _, l := range lists {
		in := saveGrIn(l.section, saveGrAdd("new-rule", "error", "New rule."))
		notTable := func(key string) func(t *testing.T, err error) {
			return func(t *testing.T, err error) {
				var de *mcpserver.DataError
				if !errors.As(err, &de) || !strings.Contains(de.Msg, key+" in ") || !strings.Contains(de.Msg, "is not a table") {
					t.Errorf("err = %v, want a DataError that %s is not a table", err, key)
					return
				}
				if want := "Fix " + key + " in config.toml manually."; de.Suggestion != want {
					t.Errorf("suggestion = %q, want %q", de.Suggestion, want)
				}
			}
		}
		arrayForm := func(t *testing.T, err error) {
			var de *mcpserver.DomainError
			if !errors.As(err, &de) {
				t.Fatalf("err = %v, want a DomainError", err)
			}
			wantMsg := saveRequestMsg(fmt.Sprintf("config.toml holds %s as an array ([[%s]] or an inline array). The dashboard edits only [%s.<id>] tables", l.path, l.path, l.path))
			if de.Msg != wantMsg {
				t.Errorf("msg = %q, want %q", de.Msg, wantMsg)
			}
			if want := fmt.Sprintf("Change the list in config.toml to [%s.<id>] tables manually, then reload the page.", l.path); de.Suggestion != want {
				t.Errorf("suggestion = %q, want %q", de.Suggestion, want)
			}
		}
		ok := func(t *testing.T, err error) {
			if err != nil {
				t.Errorf("err = %v, want nil", err)
			}
		}
		tableStates := []struct {
			name, config string
			check        func(t *testing.T, err error)
			unchanged    bool
		}{
			{"no parent table", "[jira]\ndefaultProject = \"X\"\n", ok, false},
			{"no guardrails key", "[" + l.parent + "]\n", ok, false},
			{"parent is a string", l.parent + " = \"x\"\n", notTable(l.parent), true},
			{"guardrails is a string", "[" + l.parent + "]\nguardrails = \"x\"\n", notTable(l.path), true},
			{"entry is not a table", "[" + l.path + "]\ndry = \"x\"\n", notTable(l.path + ".dry"), true},
			{"array of tables", "[[" + l.path + "]]\nid = \"dry\"\ndescription = \"Keep code DRY.\"\n", arrayForm, true},
			{"inline array", "[" + l.parent + "]\nguardrails = [{ id = \"dry\", description = \"Keep code DRY.\" }]\n", arrayForm, true},
		}
		for _, ts := range tableStates {
			t.Run(l.section+"/"+ts.name, func(t *testing.T) {
				root, userPath := saveRepo(t, ts.config, "", "")
				before := saveSnapshot(saveConfigPath(root), saveLocalPath(root), userPath)
				_, err := DashboardSetupSave(root, in)
				ts.check(t, err)
				if !ts.unchanged {
					return
				}
				if got := saveSnapshot(saveConfigPath(root), saveLocalPath(root), userPath); fmt.Sprint(got) != fmt.Sprint(before) {
					t.Error("a refused save changed a file")
				}
			})
		}
	}
}
