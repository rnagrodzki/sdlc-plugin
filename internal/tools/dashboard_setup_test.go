package tools

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rnagrodzki/sdlc-plugin/internal/config"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/setupmeta"
)

// dashSetupRoot returns an empty repo root and points the user file at a
// path under a second temp dir. The user file does not exist yet.
func dashSetupRoot(t *testing.T) (root, userPath string) {
	t.Helper()
	root = t.TempDir()
	userPath = filepath.Join(t.TempDir(), "user-local.toml")
	t.Setenv(config.UserConfigPathEnv, userPath)
	return root, userPath
}

// dashSetupWrite writes content to path and makes the parent folder.
func dashSetupWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// dashSetupState calls DashboardSetupState and fails the test on an error.
func dashSetupState(t *testing.T, root string) DashboardSetupStateOut {
	t.Helper()
	out, err := DashboardSetupState(root)
	if err != nil {
		t.Fatalf("DashboardSetupState: %v", err)
	}
	return out
}

// dashSetupErr calls DashboardSetupState and returns its error.
func dashSetupErr(t *testing.T, root string) error {
	t.Helper()
	_, err := DashboardSetupState(root)
	if err == nil {
		t.Fatal("DashboardSetupState: want an error, got none")
	}
	return err
}

// dashSetupSuggestion returns the Suggestion of a DataError or InfraError.
func dashSetupSuggestion(err error) (kind, msg, suggestion string) {
	var data *mcpserver.DataError
	var infra *mcpserver.InfraError
	switch {
	case errors.As(err, &data):
		return "data", data.Msg, data.Suggestion
	case errors.As(err, &infra):
		return "infra", infra.Msg, infra.Suggestion
	}
	return "other", err.Error(), ""
}

// dashSetupDelegated returns the delegated row with the id.
func dashSetupDelegated(t *testing.T, out DashboardSetupStateOut, id string) DashboardDelegatedState {
	t.Helper()
	for _, d := range out.Delegated {
		if d.ID == id {
			return d
		}
	}
	t.Fatalf("no delegated row %q in %+v", id, out.Delegated)
	return DashboardDelegatedState{}
}

func TestDashboardSetupFields(t *testing.T) {
	out := DashboardSetupFields()
	meta := setupmeta.Sections()
	if len(out.Sections) != 19 {
		t.Fatalf("sections = %d, want 19", len(out.Sections))
	}
	choice := 0
	for i, sec := range out.Sections {
		if sec.ID != meta[i].ID {
			t.Errorf("section %d is %q, want %q", i, sec.ID, meta[i].ID)
		}
		for _, f := range sec.Fields {
			if f.Details == "" {
				t.Errorf("%s.%s has no details", sec.ID, f.Name)
			}
			switch f.Type {
			case "enum", "multi-enum", "flag-set", "boolean":
				choice++
				if len(f.Effects) == 0 {
					t.Errorf("%s.%s has no effects", sec.ID, f.Name)
				}
			default:
				if len(f.Effects) != 0 {
					t.Errorf("%s.%s has effects, want none", sec.ID, f.Name)
				}
			}
		}
	}
	if choice != 24 {
		t.Errorf("choice fields = %d, want 24", choice)
	}
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	for _, sec := range []string{`"details"`, `"effects"`} {
		if !strings.Contains(string(b), sec) {
			t.Errorf("the field list JSON has no key %s", sec)
		}
	}
	if strings.Contains(string(b), `"fields":null`) {
		t.Error("a section has fields null, want []")
	}
}

func TestDashboardSetupState(t *testing.T) {
	root, userPath := dashSetupRoot(t)
	dir := filepath.Join(root, ".sdlc-v2")
	dashSetupWrite(t, filepath.Join(dir, "config.toml"), `[version.tag]
enabled = true
prefix = "v"
[commit]
subjectPattern = "x"
types = ["feat"]
[pr]
titlePattern = "y"
[pr.labels]
mode = "rules"
[plan]
guardrails = [{id = "a"}, {id = "b"}, {id = "c"}]
[execute]
guardrails = [{id = "d"}]
`)
	dashSetupWrite(t, filepath.Join(dir, "local.toml"), "[ship]\nbump = \"minor\"\n")
	dashSetupWrite(t, userPath, "[ship]\nbump = \"patch\"\ndraft = true\n")
	dashSetupWrite(t, filepath.Join(dir, "review-dimensions", "a.md"), "---\nname: a\n---\nbody\n")
	dashSetupWrite(t, filepath.Join(dir, "pr-template.md"), "x")
	dashSetupWrite(t, filepath.Join(root, "openspec", "config.yaml"), "# BEGIN MANAGED BY sdlc-v2 (v2)\ncontext: x\n# END MANAGED BY sdlc-v2 (v2)\n")

	out := dashSetupState(t, root)
	if out.State != SetupStateReady || out.Root != root {
		t.Fatalf("state = %q root = %q", out.State, out.Root)
	}
	if out.Files.Config != filepath.Join(dir, "config.toml") || out.Files.Project != filepath.Join(dir, "local.toml") || out.Files.User != userPath {
		t.Errorf("files = %+v", out.Files)
	}
	if out.ConfigError != "" {
		t.Errorf("configError = %q, want empty", out.ConfigError)
	}

	// One key for each of the 10 form sections.
	if len(out.Values) != 10 {
		t.Errorf("values has %d sections, want 10: %v", len(out.Values), out.Values)
	}
	for _, id := range []string{"version", "ship", "jira", "review", "received-review", "github", "communication-style", "plan-style", "plan-tasks", "automation"} {
		if out.Values[id] == nil {
			t.Errorf("values[%q] is missing or nil", id)
		}
	}
	if got := out.Values["version"]["tag.prefix"]; got.Source != "config" || got.Value != "v" {
		t.Errorf("version tag.prefix = %+v", got)
	}
	if got := out.Values["version"]["tag.enabled"]; got.Source != "config" || got.Value != true {
		t.Errorf("version tag.enabled = %+v, want true from config", got)
	}
	if got := out.Values["ship"]["bump"]; got.Source != "project" || got.Value != "minor" {
		t.Errorf("ship.bump = %+v, want minor from project", got)
	}
	if got := out.Values["ship"]["draft"]; got.Source != "user" || got.Value != true {
		t.Errorf("ship.draft = %+v, want true from user", got)
	}
	if len(out.Values["jira"]) != 0 {
		t.Errorf("jira values = %v, want none", out.Values["jira"])
	}

	two, one, three := 2, 1, 3
	wantCount := map[string]*int{"commit": &two, "pr": &one, "review-dimensions": &one, "plan-guardrails": &three, "execution-guardrails": &one}
	if len(out.Delegated) != 9 {
		t.Fatalf("delegated = %d rows, want 9", len(out.Delegated))
	}
	for id, want := range wantCount {
		d := dashSetupDelegated(t, out, id)
		if !d.Set || d.Count == nil || *d.Count != *want {
			t.Errorf("%s = %+v, want set with count %d", id, d, *want)
		}
	}
	if d := dashSetupDelegated(t, out, "pr-labels"); !d.Set || d.Mode != "rules" || d.Count != nil {
		t.Errorf("pr-labels = %+v", d)
	}
	if d := dashSetupDelegated(t, out, "pr-template"); !d.Set {
		t.Errorf("pr-template = %+v, want set", d)
	}
	if d := dashSetupDelegated(t, out, "plan-template"); d.Set {
		t.Errorf("plan-template = %+v, want not set", d)
	}
	if d := dashSetupDelegated(t, out, "openspec-block"); !d.Set {
		t.Errorf("openspec-block = %+v, want set", d)
	}
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "null") {
		// "value" may be a JSON null only for a TOML value that is not null; none here.
		t.Errorf("state JSON holds null: %s", b)
	}
}

func TestDashboardSetupState_ProjectWinsOverUser(t *testing.T) {
	root, userPath := dashSetupRoot(t)
	dashSetupWrite(t, filepath.Join(root, ".sdlc-v2", "config.toml"), "[version.tag]\nenabled = true\n")
	dashSetupWrite(t, filepath.Join(root, ".sdlc-v2", "local.toml"), "[ship]\nbump = \"minor\"\n")
	dashSetupWrite(t, userPath, "[ship]\nbump = \"patch\"\n")
	out := dashSetupState(t, root)
	if got := out.Values["ship"]["bump"]; got.Source != "project" || got.Value != "minor" {
		t.Errorf("ship.bump = %+v, want minor from project", got)
	}
}

func TestDashboardSetupState_EmptyConfig(t *testing.T) {
	root, _ := dashSetupRoot(t)
	dashSetupWrite(t, filepath.Join(root, ".sdlc-v2", "config.toml"), "")
	out := dashSetupState(t, root)
	if out.State != SetupStateReady {
		t.Fatalf("state = %q", out.State)
	}
	for id, values := range out.Values {
		if values == nil || len(values) != 0 {
			t.Errorf("values[%q] = %v, want {}", id, values)
		}
	}
	for _, d := range out.Delegated {
		if d.Set {
			t.Errorf("%s is set on an empty config", d.ID)
		}
	}
	if d := dashSetupDelegated(t, out, "commit"); d.Count == nil || *d.Count != 0 {
		t.Errorf("commit = %+v, want count 0", d)
	}
}

func TestDashboardSetupState_NoConfig(t *testing.T) {
	root, _ := dashSetupRoot(t)
	out := dashSetupState(t, root)
	if out.State != SetupStateNoConfig {
		t.Errorf("state = %q, want %q", out.State, SetupStateNoConfig)
	}
	if out.Delegated == nil || len(out.Delegated) != 0 {
		t.Errorf("delegated = %v, want []", out.Delegated)
	}
	if out.Values == nil || len(out.Values) != 10 {
		t.Errorf("values = %v, want 10 empty sections", out.Values)
	}
	b, _ := json.Marshal(out)
	if strings.Contains(string(b), "null") {
		t.Errorf("state JSON holds null: %s", b)
	}
}

func TestDashboardSetupState_LegacyConfig(t *testing.T) {
	root, _ := dashSetupRoot(t)
	dashSetupWrite(t, filepath.Join(root, ".sdlc-v2", "config.json"), "{}")
	out := dashSetupState(t, root)
	if out.State != SetupStateLegacy {
		t.Errorf("state = %q, want %q", out.State, SetupStateLegacy)
	}
	if len(out.Delegated) != 0 {
		t.Errorf("delegated = %v, want []", out.Delegated)
	}
}

// The failure table of the plan: one test for each row.

// dashSetupEachFile runs fn as a subtest for each of the three config files.
// Each subtest gets its own repo root and user file path, so no cleanup is needed.
func dashSetupEachFile(t *testing.T, fn func(t *testing.T, root, file string)) {
	t.Helper()
	pick := map[string]func(root, userPath string) string{
		"config.toml":     func(root, _ string) string { return filepath.Join(root, ".sdlc-v2", "config.toml") },
		"local.toml":      func(root, _ string) string { return filepath.Join(root, ".sdlc-v2", "local.toml") },
		"user-local.toml": func(_, userPath string) string { return userPath },
	}
	for _, name := range []string{"config.toml", "local.toml", "user-local.toml"} {
		t.Run(name, func(t *testing.T) {
			root, userPath := dashSetupRoot(t)
			fn(t, root, pick[name](root, userPath))
		})
	}
}

func TestDashboardSetupState_FileNotValidTOML(t *testing.T) {
	dashSetupEachFile(t, func(t *testing.T, root, file string) {
		dashSetupWrite(t, file, "[ship")
		kind, msg, suggestion := dashSetupSuggestion(dashSetupErr(t, root))
		if kind != "data" || !strings.Contains(msg, file) || suggestion == "" {
			t.Errorf("error = %s %q suggestion %q, want a DataError that names %s", kind, msg, suggestion, file)
		}
	})
}

func TestDashboardSetupState_FileOverSizeLimit(t *testing.T) {
	big := strings.Repeat("#", maxCheckedFileBytes+1)
	dashSetupEachFile(t, func(t *testing.T, root, file string) {
		dashSetupWrite(t, file, big)
		kind, msg, suggestion := dashSetupSuggestion(dashSetupErr(t, root))
		if kind != "data" || !strings.Contains(msg, file) || !strings.Contains(msg, "1048577") || !strings.Contains(msg, "1048576") || suggestion == "" {
			t.Errorf("error = %s %q suggestion %q, want size, limit and file", kind, msg, suggestion)
		}
	})
}

func TestDashboardSetupState_FileIsDirectory(t *testing.T) {
	dashSetupEachFile(t, func(t *testing.T, root, file string) {
		if err := os.MkdirAll(file, 0o755); err != nil {
			t.Fatal(err)
		}
		kind, msg, suggestion := dashSetupSuggestion(dashSetupErr(t, root))
		if kind != "infra" || !strings.Contains(msg, file) || suggestion == "" {
			t.Errorf("error = %s %q suggestion %q, want an InfraError that names %s", kind, msg, suggestion, file)
		}
	})
}

func TestDashboardSetupState_FilesAbsent(t *testing.T) {
	root, userPath := dashSetupRoot(t)
	out := dashSetupState(t, root)
	if out.Files.User != userPath {
		t.Errorf("files.user = %q", out.Files.User)
	}
	for id, values := range out.Values {
		if len(values) != 0 {
			t.Errorf("values[%q] = %v, want none", id, values)
		}
	}
}

func TestDashboardSetupState_SectionNotATable(t *testing.T) {
	root, _ := dashSetupRoot(t)
	local := filepath.Join(root, ".sdlc-v2", "local.toml")
	dashSetupWrite(t, filepath.Join(root, ".sdlc-v2", "config.toml"), "[version.tag]\nenabled = true\n")
	dashSetupWrite(t, local, "ship = \"x\"\n")
	out := dashSetupState(t, root)
	if v := out.Values["ship"]; v == nil || len(v) != 0 {
		t.Errorf("values[ship] = %v, want {}", v)
	}
	if want := local + ": ship is not a table."; !strings.Contains(out.ConfigError, want) {
		t.Errorf("configError = %q, want it to contain %q", out.ConfigError, want)
	}
	if out.State != SetupStateReady {
		t.Errorf("state = %q, want ready", out.State)
	}
}

func TestDashboardSetupState_ParentNotATable(t *testing.T) {
	root, _ := dashSetupRoot(t)
	cfg := filepath.Join(root, ".sdlc-v2", "config.toml")
	dashSetupWrite(t, cfg, "[version]\ntag = \"x\"\n")
	out := dashSetupState(t, root)
	if _, ok := out.Values["version"]["tag.enabled"]; ok {
		t.Error("version has a value for tag.enabled, want none")
	}
	want := cfg + ": version.tag is not a table."
	if strings.Count(out.ConfigError, want) != 1 {
		t.Errorf("configError = %q, want it to contain %q once", out.ConfigError, want)
	}
}

func TestDashboardSetupState_ConfigReadError(t *testing.T) {
	root, _ := dashSetupRoot(t)
	dashSetupWrite(t, filepath.Join(root, ".sdlc-v2", "config.toml"), "[version]\n")
	out := dashSetupState(t, root)
	if out.State != SetupStateReady {
		t.Errorf("state = %q, want ready", out.State)
	}
	if out.ConfigError == "" {
		t.Error("configError is empty, want the text of the config.Read error")
	}
}

func TestDashboardSetupState_OpenspecAbsent(t *testing.T) {
	root, _ := dashSetupRoot(t)
	dashSetupWrite(t, filepath.Join(root, ".sdlc-v2", "config.toml"), "")
	if d := dashSetupDelegated(t, dashSetupState(t, root), "openspec-block"); d.Set {
		t.Errorf("openspec-block = %+v, want not set", d)
	}
}

func TestDashboardSetupState_OpenspecOverSizeLimit(t *testing.T) {
	root, _ := dashSetupRoot(t)
	dashSetupWrite(t, filepath.Join(root, ".sdlc-v2", "config.toml"), "")
	file := filepath.Join(root, "openspec", "config.yaml")
	dashSetupWrite(t, file, strings.Repeat("#", maxCheckedFileBytes+1))
	kind, msg, suggestion := dashSetupSuggestion(dashSetupErr(t, root))
	if kind != "data" || !strings.Contains(msg, file) || !strings.Contains(msg, "1048577") || !strings.Contains(msg, "1048576") || suggestion == "" {
		t.Errorf("error = %s %q suggestion %q", kind, msg, suggestion)
	}
}

func TestDashboardSetupState_OpenspecReadError(t *testing.T) {
	root, _ := dashSetupRoot(t)
	dashSetupWrite(t, filepath.Join(root, ".sdlc-v2", "config.toml"), "")
	file := filepath.Join(root, "openspec", "config.yaml")
	if err := os.MkdirAll(file, 0o755); err != nil {
		t.Fatal(err)
	}
	kind, msg, suggestion := dashSetupSuggestion(dashSetupErr(t, root))
	if kind != "infra" || !strings.Contains(msg, file) || suggestion == "" {
		t.Errorf("error = %s %q suggestion %q", kind, msg, suggestion)
	}
}

func TestDashboardSetupState_DimensionsLoadError(t *testing.T) {
	root, _ := dashSetupRoot(t)
	dashSetupWrite(t, filepath.Join(root, ".sdlc-v2", "config.toml"), "")
	// A regular file where the folder belongs: os.ReadDir returns an error.
	dashSetupWrite(t, filepath.Join(root, ".sdlc-v2", "review-dimensions"), "x")
	kind, msg, suggestion := dashSetupSuggestion(dashSetupErr(t, root))
	if kind != "infra" || !strings.Contains(msg, "review-dimensions") || !strings.Contains(suggestion, ".sdlc-v2") {
		t.Errorf("error = %s %q suggestion %q", kind, msg, suggestion)
	}
}

func TestDashboardSetupState_TemplateStatError(t *testing.T) {
	root, _ := dashSetupRoot(t)
	dir := filepath.Join(root, ".sdlc-v2")
	dashSetupWrite(t, filepath.Join(dir, "config.toml"), "")
	// A link to itself: os.Stat returns a loop error, not a missing-file error.
	if err := os.Symlink("pr-template.md", filepath.Join(dir, "pr-template.md")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	kind, msg, suggestion := dashSetupSuggestion(dashSetupErr(t, root))
	if kind != "infra" || !strings.Contains(msg, "pr-template.md") || !strings.Contains(suggestion, ".sdlc-v2") {
		t.Errorf("error = %s %q suggestion %q", kind, msg, suggestion)
	}
}

func TestDashboardSetupState_NoUserFilePath(t *testing.T) {
	root, _ := dashSetupRoot(t)
	t.Setenv(config.UserConfigPathEnv, "")
	// os.UserHomeDir reads HOME (Unix), USERPROFILE (Windows) and home (Plan 9).
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	t.Setenv("home", "")
	dashSetupWrite(t, filepath.Join(root, ".sdlc-v2", "config.toml"), "")
	if _, ok := config.UserConfigPath(); ok {
		t.Fatal("the user file path still resolves with every home variable empty")
	}
	out := dashSetupState(t, root)
	if out.Files.User != "" {
		t.Errorf("files.user = %q, want empty", out.Files.User)
	}
}
