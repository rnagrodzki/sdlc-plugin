package tools

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
		saveSet("automation", "drift.minErrorFloor", float64(500)),
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

// TestDashboardSetupSave_RequestRules covers each row of the request rule
// table. Each case returns a DomainError that names the rule, and no file
// changes.
func TestDashboardSetupSave_RequestRules(t *testing.T) {
	tooMany := make([]DashboardSetupChange, maxSetupChanges+1)
	for i := range tooMany {
		tooMany[i] = DashboardSetupChange{Section: "nope", Field: "x", Op: "bad"}
	}
	cases := []struct {
		name string
		in   DashboardSetupSaveIn
		want string
	}{
		{"absent changes", DashboardSetupSaveIn{}, "changes is empty"},
		{"empty changes", DashboardSetupSaveIn{Changes: []DashboardSetupChange{}}, "changes is empty"},
		{"too many changes", DashboardSetupSaveIn{Changes: tooMany}, fmt.Sprintf("changes has %d entries, over the limit of %d", maxSetupChanges+1, maxSetupChanges)},
		{"unknown section", DashboardSetupSaveIn{Changes: []DashboardSetupChange{saveSet("nope", "x", "y")}}, `section "nope" is not a form section`},
		{"delegated section", DashboardSetupSaveIn{Changes: []DashboardSetupChange{saveSet("commit", "allowedTypes", []any{"feat"})}}, `section "commit" is a delegated section`},
		{"unknown field", DashboardSetupSaveIn{Changes: []DashboardSetupChange{saveSet("ship", "nope", "y")}}, `field "nope" is not a field of section "ship"`},
		{"same field twice", DashboardSetupSaveIn{Changes: []DashboardSetupChange{saveSet("ship", "bump", "minor"), saveReset("ship", "bump")}}, `section "ship" field "bump" is in changes more than once`},
		{"bad op", DashboardSetupSaveIn{Changes: []DashboardSetupChange{{Section: "ship", Field: "bump", Op: "delete"}}}, `op "delete" is not set or reset`},
		{"set with no value", DashboardSetupSaveIn{Changes: []DashboardSetupChange{{Section: "ship", Field: "bump", Op: "set"}}}, "use op reset to remove a value"},
		{"reset with a value", DashboardSetupSaveIn{Changes: []DashboardSetupChange{{Section: "ship", Field: "bump", Op: "reset", Value: "minor"}}}, `section "ship" field "bump" has op reset with a value`},
		{"target of a config.toml section", DashboardSetupSaveIn{Changes: []DashboardSetupChange{saveSet("jira", "defaultProject", "A")}, Targets: map[string]string{"jira": "user"}}, `targets names section "jira"`},
		{"target with no change", DashboardSetupSaveIn{Changes: []DashboardSetupChange{saveSet("ship", "bump", "minor")}, Targets: map[string]string{"review": "user"}}, `targets names section "review"`},
		{"bad target value", DashboardSetupSaveIn{Changes: []DashboardSetupChange{saveSet("ship", "bump", "minor")}, Targets: map[string]string{"ship": "both"}}, `target "both" is not project or user`},
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
			if !strings.Contains(de.Msg, tc.want) || de.Suggestion == "" {
				t.Errorf("msg = %q, suggestion = %q; want the msg to name %q", de.Msg, de.Suggestion, tc.want)
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
	return "x"
}

// TestDashboardSetupSave_AllFields checks that a save of all 48 form fields
// passes the request rules and the value check.
func TestDashboardSetupSave_AllFields(t *testing.T) {
	root, _ := saveRepo(t, "", "", "")
	var changes []DashboardSetupChange
	for _, sec := range setupmeta.Sections() {
		if sec.DelegatedTo != "" || sec.ConfigPath == "" {
			continue
		}
		for _, f := range sec.Fields {
			changes = append(changes, saveSet(sec.ID, f.Name, saveFieldValue(sec, f)))
		}
	}
	if len(changes) != 48 {
		t.Fatalf("the form has %d fields, want 48", len(changes))
	}
	out := saveOK(t, root, DashboardSetupSaveIn{Changes: changes})
	if len(out.Written) != 10 {
		t.Errorf("written = %+v, want 10 sections", out.Written)
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
			return SetupWriteSectionsOut{OK: false, Written: nil, Errors: []string{"section ship: disk full"}}, nil
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
// project file write fails after the user file write.
func TestDashboardSetupSave_SecondWriteFails(t *testing.T) {
	root, userPath := saveRepo(t, "[jira]\ndefaultProject = \"X\"\n", "[ship]\nbump = \"patch\"\n", "")
	replaceSetupWrite(t, func(call int, contentRoot, stateRoot string, in SetupWriteSectionsIn) (SetupWriteSectionsOut, error) {
		if call == 2 {
			return SetupWriteSectionsOut{}, &mcpserver.InfraError{Msg: "write refused"}
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
		{Section: "jira", File: saveConfigPath(root), Message: "write refused"},
		{Section: "ship", File: saveLocalPath(root), Message: "write refused"},
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
		if !out.OK || len(out.Errors) != 0 {
			t.Fatalf("out = %+v", out)
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
