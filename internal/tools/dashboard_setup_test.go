package tools

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
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

// dashSetupIsChoice reports whether a field of type typ is a choice field:
// a field that has one effect for each option.
func dashSetupIsChoice(typ string) bool {
	switch typ {
	case "enum", "multi-enum", "flag-set", "boolean":
		return true
	}
	return false
}

// dashSetupFormIDs returns the ids of the form sections of
// setupmeta.Sections(), in order: each section with a config path and at
// least one field.
func dashSetupFormIDs() []string {
	ids := []string{}
	for _, sec := range setupmeta.Sections() {
		if sec.ConfigPath != "" && len(sec.Fields) > 0 {
			ids = append(ids, sec.ID)
		}
	}
	return ids
}

// dashSetupDelegatedIDs returns the ids of the delegated sections of
// setupmeta.Sections(), in order.
func dashSetupDelegatedIDs() []string {
	ids := []string{}
	for _, sec := range setupmeta.Sections() {
		if sec.DelegatedTo != "" {
			ids = append(ids, sec.ID)
		}
	}
	return ids
}

// dashSetupWantConfigError returns the ConfigError that DashboardSetupState
// gives for root: the text of the config.Read error first, then each note,
// joined with one space.
func dashSetupWantConfigError(root string, notes ...string) string {
	var parts []string
	if _, err := config.Read(root); err != nil {
		parts = append(parts, err.Error())
	}
	return strings.Join(append(parts, notes...), " ")
}

// dashSetupConfig writes content to .sdlc-v2/config.toml of root and returns
// the path of the file.
func dashSetupConfig(t *testing.T, root, content string) string {
	t.Helper()
	cfg := filepath.Join(root, ".sdlc-v2", "config.toml")
	dashSetupWrite(t, cfg, content)
	return cfg
}

func TestDashboardSetupFields(t *testing.T) {
	out := DashboardSetupFields()
	meta := setupmeta.Sections()
	if len(meta) == 0 {
		t.Fatal("setupmeta.Sections() is empty")
	}
	if len(out.Sections) != len(meta) {
		t.Fatalf("sections = %d, want %d (setupmeta.Sections())", len(out.Sections), len(meta))
	}
	wantChoice := 0
	for _, sec := range meta {
		for _, f := range sec.Fields {
			if dashSetupIsChoice(f.Type) {
				wantChoice++
			}
		}
	}
	choice := 0
	for i, sec := range out.Sections {
		if sec.ID != meta[i].ID {
			t.Errorf("section %d is %q, want %q", i, sec.ID, meta[i].ID)
		}
		if len(sec.Fields) != len(meta[i].Fields) {
			t.Errorf("section %q has %d fields, want %d", sec.ID, len(sec.Fields), len(meta[i].Fields))
		}
		for _, f := range sec.Fields {
			if f.Details == "" {
				t.Errorf("%s.%s has no details", sec.ID, f.Name)
			}
			switch {
			case dashSetupIsChoice(f.Type):
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
	if wantChoice == 0 || choice != wantChoice {
		t.Errorf("choice fields = %d, want %d (setupmeta.Sections())", choice, wantChoice)
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

	// One key for each form section of setupmeta.Sections().
	formIDs := dashSetupFormIDs()
	if len(formIDs) == 0 || len(out.Values) != len(formIDs) {
		t.Errorf("values has %d sections, want %d: %v", len(out.Values), len(formIDs), out.Values)
	}
	for _, id := range formIDs {
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
	delegatedIDs := dashSetupDelegatedIDs()
	if len(delegatedIDs) == 0 || len(out.Delegated) != len(delegatedIDs) {
		t.Fatalf("delegated = %d rows, want %d", len(out.Delegated), len(delegatedIDs))
	}
	for i, d := range out.Delegated {
		if d.ID != delegatedIDs[i] {
			t.Errorf("delegated row %d is %q, want %q (setupmeta.Sections() order)", i, d.ID, delegatedIDs[i])
		}
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
	if want := len(dashSetupFormIDs()); out.Values == nil || len(out.Values) != want {
		t.Errorf("values = %v, want %d empty sections", out.Values, want)
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
		wantMsg := fmt.Sprintf("dashboard setup: %s is %d bytes, over the %d byte limit of the Setup tab", file, maxCheckedFileBytes+1, maxCheckedFileBytes)
		wantSuggestion := fmt.Sprintf("Reduce %s to %d bytes or less, then retry.", file, maxCheckedFileBytes)
		if kind != "data" || msg != wantMsg || suggestion != wantSuggestion {
			t.Errorf("error = %s %q suggestion %q, want data %q suggestion %q", kind, msg, suggestion, wantMsg, wantSuggestion)
		}
	})
}

// A file of exactly maxCheckedFileBytes bytes is read: the limit is not an
// error. The content is one TOML comment line, so it is valid TOML.
func TestDashboardSetupState_FileAtSizeLimit(t *testing.T) {
	atLimit := strings.Repeat("#", maxCheckedFileBytes)
	dashSetupEachFile(t, func(t *testing.T, root, file string) {
		dashSetupWrite(t, file, atLimit)
		if info, err := os.Stat(file); err != nil || info.Size() != maxCheckedFileBytes {
			t.Fatalf("stat %s = %v, %v; want %d bytes", file, info, err, maxCheckedFileBytes)
		}
		out := dashSetupState(t, root)
		for id, values := range out.Values {
			if len(values) != 0 {
				t.Errorf("values[%q] = %v, want none from a comment-only file", id, values)
			}
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
	wantMsg := fmt.Sprintf("dashboard setup: %s is %d bytes, over the %d byte limit", file, maxCheckedFileBytes+1, maxCheckedFileBytes)
	wantSuggestion := fmt.Sprintf("Reduce %s to %d bytes or less, then reload the tab.", file, maxCheckedFileBytes)
	if kind != "data" || msg != wantMsg || suggestion != wantSuggestion {
		t.Errorf("error = %s %q suggestion %q, want data %q suggestion %q", kind, msg, suggestion, wantMsg, wantSuggestion)
	}
}

// An openspec/config.yaml of exactly maxCheckedFileBytes bytes is read: with
// no managed block the row is not set, with the block it is set.
func TestDashboardSetupState_OpenspecAtSizeLimit(t *testing.T) {
	block := "# BEGIN MANAGED BY sdlc-v2 (v2)\ncontext: x\n# END MANAGED BY sdlc-v2 (v2)\n"
	for _, tc := range []struct {
		name    string
		content string
		wantSet bool
	}{
		{"no block", strings.Repeat("#", maxCheckedFileBytes), false},
		{"with block", block + strings.Repeat("#", maxCheckedFileBytes-len(block)), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, _ := dashSetupRoot(t)
			dashSetupConfig(t, root, "")
			file := filepath.Join(root, "openspec", "config.yaml")
			dashSetupWrite(t, file, tc.content)
			if info, err := os.Stat(file); err != nil || info.Size() != maxCheckedFileBytes {
				t.Fatalf("stat %s = %v, %v; want %d bytes", file, info, err, maxCheckedFileBytes)
			}
			if d := dashSetupDelegated(t, dashSetupState(t, root), "openspec-block"); d.Set != tc.wantSet {
				t.Errorf("openspec-block = %+v, want set %v", d, tc.wantSet)
			}
		})
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

// setupGuardrailCount counts each arm of the decoded guardrails value: a
// list, a list of tables, and the named-table form, where only the entries
// that hold a table count.
func TestSetupGuardrailCount(t *testing.T) {
	for _, tc := range []struct {
		name     string
		section  map[string]any
		want     int
		wantNote bool
	}{
		{"absent", map[string]any{}, 0, false},
		{"list", map[string]any{"guardrails": []any{map[string]any{"id": "a"}, map[string]any{"id": "b"}}}, 2, false},
		{"empty list", map[string]any{"guardrails": []any{}}, 0, false},
		{"list of tables", map[string]any{"guardrails": []map[string]any{{"id": "a"}, {"id": "b"}, {"id": "c"}}}, 3, false},
		{"named tables", map[string]any{"guardrails": map[string]any{"a": map[string]any{}, "b": map[string]any{"x": 1}, "note": "x"}}, 2, false},
		{"text", map[string]any{"guardrails": "x"}, 0, true},
		{"number", map[string]any{"guardrails": int64(3)}, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var notes []string
			got := setupGuardrailCount(tc.section, "plan.guardrails", "/r/config.toml", func(s string) { notes = append(notes, s) })
			if got != tc.want {
				t.Errorf("count = %d, want %d", got, tc.want)
			}
			wantNotes := []string(nil)
			if tc.wantNote {
				wantNotes = []string{"/r/config.toml: plan.guardrails is not a list or a table."}
			}
			if strings.Join(notes, "|") != strings.Join(wantNotes, "|") {
				t.Errorf("notes = %q, want %q", notes, wantNotes)
			}
		})
	}
}

// Guardrails in the named-table form ([plan.guardrails.<id>]) and in the
// array-of-tables form ([[plan.guardrails]]) count, and the row is set.
func TestDashboardSetupState_GuardrailForms(t *testing.T) {
	for _, tc := range []struct {
		name    string
		content string
		plan    int
		execute int
	}{
		{"named tables", "[plan.guardrails]\nnote = \"x\"\n[plan.guardrails.a]\ndescription = \"a\"\n[plan.guardrails.b]\ndescription = \"b\"\n" +
			"[execute.guardrails.c]\ndescription = \"c\"\n", 2, 1},
		{"array of tables", "[[plan.guardrails]]\nid = \"a\"\n[[plan.guardrails]]\nid = \"b\"\n[[plan.guardrails]]\nid = \"c\"\n" +
			"[[execute.guardrails]]\nid = \"d\"\n[[execute.guardrails]]\nid = \"e\"\n", 3, 2},
		{"inline list", "[plan]\nguardrails = [{id = \"a\"}]\n[execute]\nguardrails = []\n", 1, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, _ := dashSetupRoot(t)
			dashSetupConfig(t, root, tc.content)
			out := dashSetupState(t, root)
			for id, want := range map[string]int{"plan-guardrails": tc.plan, "execution-guardrails": tc.execute} {
				d := dashSetupDelegated(t, out, id)
				if d.Count == nil || *d.Count != want || d.Set != (want > 0) {
					t.Errorf("%s = %+v, want count %d and set %v", id, d, want, want > 0)
				}
			}
			if strings.Contains(out.ConfigError, "guardrails is not") {
				t.Errorf("configError = %q, want no guardrails note", out.ConfigError)
			}
		})
	}
}

// A value of config.toml that does not have the type a delegated row reads
// gives the exact note in ConfigError, after the config.Read error, and the
// row counts it as not set.
func TestDashboardSetupState_DelegatedWrongType(t *testing.T) {
	for _, tc := range []struct {
		name    string
		content string
		row     string
		notes   []string // each is appended to "<config.toml path>: "
	}{
		{"commit", "commit = \"x\"\n", "commit", []string{"commit is not a table."}},
		// pr-labels reads pr.labels under pr, so it gives a second note.
		{"pr", "pr = \"x\"\n", "pr", []string{"pr is not a table.", "pr.labels is not a table."}},
		{"pr.labels", "[pr]\nlabels = \"x\"\n", "pr-labels", []string{"pr.labels is not a table."}},
		// plan-tasks is a form section at plan.tasks, so its note comes first.
		{"plan", "plan = \"x\"\n", "plan-guardrails", []string{"plan.tasks is not a table.", "plan is not a table."}},
		{"execute", "execute = \"x\"\n", "execution-guardrails", []string{"execute is not a table."}},
		{"plan.guardrails text", "[plan]\nguardrails = \"x\"\n", "plan-guardrails", []string{"plan.guardrails is not a list or a table."}},
		{"execute.guardrails number", "[execute]\nguardrails = 5\n", "execution-guardrails", []string{"execute.guardrails is not a list or a table."}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, _ := dashSetupRoot(t)
			cfg := dashSetupConfig(t, root, tc.content)
			out := dashSetupState(t, root)
			notes := make([]string, len(tc.notes))
			for i, n := range tc.notes {
				notes[i] = cfg + ": " + n
			}
			if want := dashSetupWantConfigError(root, notes...); out.ConfigError != want {
				t.Errorf("configError = %q\nwant %q", out.ConfigError, want)
			}
			d := dashSetupDelegated(t, out, tc.row)
			if d.Set {
				t.Errorf("%s = %+v, want not set", tc.row, d)
			}
			if d.Count != nil && *d.Count != 0 {
				t.Errorf("%s count = %d, want 0", tc.row, *d.Count)
			}
		})
	}
}

// A pr.labels.mode that is not a text gives a note, and the row has no mode.
// The table holds a second key, so the row stays set.
func TestDashboardSetupState_PRLabelsModeNotText(t *testing.T) {
	root, _ := dashSetupRoot(t)
	cfg := dashSetupConfig(t, root, "[pr.labels]\nmode = 3\nexclude = [\"wip\"]\n")
	out := dashSetupState(t, root)
	if want := dashSetupWantConfigError(root, cfg+": pr.labels.mode is not a text."); out.ConfigError != want {
		t.Errorf("configError = %q\nwant %q", out.ConfigError, want)
	}
	if d := dashSetupDelegated(t, out, "pr-labels"); !d.Set || d.Mode != "" || d.Count != nil {
		t.Errorf("pr-labels = %+v, want set with no mode and no count", d)
	}
}

// Edge cases of the delegated rows that give no note.
func TestDashboardSetupState_DelegatedEdgeCases(t *testing.T) {
	t.Run("pr holds only labels", func(t *testing.T) {
		root, _ := dashSetupRoot(t)
		dashSetupConfig(t, root, "[pr.labels]\nmode = \"rules\"\n")
		out := dashSetupState(t, root)
		if d := dashSetupDelegated(t, out, "pr"); d.Set || d.Count == nil || *d.Count != 0 {
			t.Errorf("pr = %+v, want count 0 and not set", d)
		}
		if d := dashSetupDelegated(t, out, "pr-labels"); !d.Set || d.Mode != "rules" {
			t.Errorf("pr-labels = %+v, want set with mode rules", d)
		}
	})
	t.Run("pr.labels with no mode", func(t *testing.T) {
		root, _ := dashSetupRoot(t)
		dashSetupConfig(t, root, "[pr.labels]\nexclude = [\"wip\"]\n")
		out := dashSetupState(t, root)
		if d := dashSetupDelegated(t, out, "pr-labels"); !d.Set || d.Mode != "" {
			t.Errorf("pr-labels = %+v, want set with no mode", d)
		}
		if strings.Contains(out.ConfigError, "pr.labels") {
			t.Errorf("configError = %q, want no pr.labels note", out.ConfigError)
		}
	})
	t.Run("empty pr.labels", func(t *testing.T) {
		root, _ := dashSetupRoot(t)
		dashSetupConfig(t, root, "[pr.labels]\n")
		if d := dashSetupDelegated(t, dashSetupState(t, root), "pr-labels"); d.Set || d.Mode != "" {
			t.Errorf("pr-labels = %+v, want not set", d)
		}
	})
	for _, tc := range []struct {
		name    string
		content string
		wantSet bool
	}{
		{"openspec without block", "context: x\n", false},
		{"openspec with block", "rules: {}\n# BEGIN MANAGED BY sdlc-v2 (v2)\ncontext: x\n# END MANAGED BY sdlc-v2 (v2)\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, _ := dashSetupRoot(t)
			dashSetupConfig(t, root, "")
			dashSetupWrite(t, filepath.Join(root, "openspec", "config.yaml"), tc.content)
			if d := dashSetupDelegated(t, dashSetupState(t, root), "openspec-block"); d.Set != tc.wantSet {
				t.Errorf("openspec-block = %+v, want set %v", d, tc.wantSet)
			}
		})
	}
}

// ConfigError is the config.Read error text, then each note, joined with one
// space; the read error comes first.
func TestDashboardSetupState_ConfigErrorText(t *testing.T) {
	t.Run("read error alone", func(t *testing.T) {
		root, _ := dashSetupRoot(t)
		dashSetupConfig(t, root, "[version]\n")
		_, readErr := config.Read(root)
		if readErr == nil {
			t.Fatal("config.Read gives no error for an empty [version] table")
		}
		if out := dashSetupState(t, root); out.ConfigError != readErr.Error() {
			t.Errorf("configError = %q, want %q", out.ConfigError, readErr.Error())
		}
	})
	t.Run("note alone", func(t *testing.T) {
		root, _ := dashSetupRoot(t)
		dashSetupConfig(t, root, "[version.tag]\nenabled = true\n")
		local := filepath.Join(root, ".sdlc-v2", "local.toml")
		dashSetupWrite(t, local, "ship = \"x\"\n")
		if _, err := config.Read(root); err != nil {
			t.Fatalf("config.Read gives an error for this input, so the note is not alone: %v", err)
		}
		if out, want := dashSetupState(t, root), local+": ship is not a table."; out.ConfigError != want {
			t.Errorf("configError = %q, want %q", out.ConfigError, want)
		}
	})
	t.Run("read error then note", func(t *testing.T) {
		root, _ := dashSetupRoot(t)
		dashSetupConfig(t, root, "[version]\n")
		local := filepath.Join(root, ".sdlc-v2", "local.toml")
		dashSetupWrite(t, local, "ship = \"x\"\n")
		_, readErr := config.Read(root)
		if readErr == nil {
			t.Fatal("config.Read gives no error for an empty [version] table")
		}
		want := readErr.Error() + " " + local + ": ship is not a table."
		if out := dashSetupState(t, root); out.ConfigError != want {
			t.Errorf("configError = %q\nwant %q", out.ConfigError, want)
		}
	})
}

// A stat of config.toml that fails with an error other than "not exist" is
// an InfraError: here .sdlc-v2 is a regular file, so the stat gives ENOTDIR.
func TestDashboardSetupState_RepoStatError(t *testing.T) {
	root, _ := dashSetupRoot(t)
	dashSetupWrite(t, filepath.Join(root, ".sdlc-v2"), "x")
	configPath := filepath.Join(root, ".sdlc-v2", "config.toml")
	err := dashSetupErr(t, root)
	var infra *mcpserver.InfraError
	if !errors.As(err, &infra) {
		t.Fatalf("error = %T %v, want *mcpserver.InfraError", err, err)
	}
	if want := "dashboard setup: stat " + configPath + ": "; !strings.HasPrefix(infra.Msg, want) {
		t.Errorf("msg = %q, want prefix %q", infra.Msg, want)
	}
	if want := "Check that " + configPath + " is readable, then reload the tab."; infra.Suggestion != want {
		t.Errorf("suggestion = %q, want %q", infra.Suggestion, want)
	}
	cause := errors.Unwrap(err)
	if cause == nil || errors.Is(cause, os.ErrNotExist) {
		t.Errorf("cause = %v, want a stat error that is not ErrNotExist", cause)
	}
	if _, stateErr := setupRepoState(root); stateErr == nil {
		t.Error("setupRepoState gives no error")
	}
}

// A read error of DashboardSetupState names the caller "dashboard setup",
// not setup_write_sections, and its Suggestion does not say that nothing
// was written: the call writes nothing.
func TestDashboardSetupState_ReadErrorNamesCaller(t *testing.T) {
	root, _ := dashSetupRoot(t)
	local := filepath.Join(root, ".sdlc-v2", "local.toml")
	dashSetupWrite(t, local, "[ship")
	kind, msg, suggestion := dashSetupSuggestion(dashSetupErr(t, root))
	if kind != "data" {
		t.Fatalf("kind = %s, want data", kind)
	}
	if want := "dashboard setup: " + local + " is not valid TOML: "; !strings.HasPrefix(msg, want) {
		t.Errorf("msg = %q, want prefix %q", msg, want)
	}
	if strings.Contains(msg, "setup_write_sections") {
		t.Errorf("msg = %q names setup_write_sections", msg)
	}
	if want := "Fix the TOML syntax of " + local + " by hand, then retry."; suggestion != want {
		t.Errorf("suggestion = %q, want %q", suggestion, want)
	}
}

// Layers hold the value of each file that sets a field, also the files that
// another file overrides. Values keep the winning file.
func TestDashboardSetupState_Layers(t *testing.T) {
	root, userPath := dashSetupRoot(t)
	dashSetupConfig(t, root, "[version.tag]\nenabled = true\n")
	dashSetupWrite(t, filepath.Join(root, ".sdlc-v2", "local.toml"), "[ship]\nbump = \"minor\"\n")
	dashSetupWrite(t, userPath, "[ship]\nbump = \"patch\"\nauto = true\n")
	out := dashSetupState(t, root)

	if got := out.Layers["ship.bump"]; len(got) != 2 || got["project"] != "minor" || got["user"] != "patch" {
		t.Errorf("layers[ship.bump] = %v, want project minor and user patch", got)
	}
	if got := out.Layers["ship.auto"]; len(got) != 1 || got["user"] != true {
		t.Errorf("layers[ship.auto] = %v, want user true only", got)
	}
	if got := out.Values["ship"]["bump"]; got.Source != "project" || got.Value != "minor" {
		t.Errorf("values ship.bump = %+v, want minor from project", got)
	}
	if got := out.Layers["version.tag.enabled"]; len(got) != 1 || got["config"] != true {
		t.Errorf("layers[version.tag.enabled] = %v, want config true only", got)
	}
	if got := out.Layers["ship.verifyPipelineMaxIterations"]; got != nil {
		t.Errorf("layers of a field no file sets = %v, want absent", got)
	}
}

// A bad parent table in the user file gives a note, also when the project
// file sets the field.
func TestDashboardSetupState_LayersBadParentInLowerFile(t *testing.T) {
	root, userPath := dashSetupRoot(t)
	dashSetupConfig(t, root, "[version.tag]\nenabled = true\n")
	dashSetupWrite(t, filepath.Join(root, ".sdlc-v2", "local.toml"), "[automation.report]\nenabled = true\n")
	dashSetupWrite(t, userPath, "[automation]\nreport = \"x\"\n")
	out := dashSetupState(t, root)

	if got := out.Values["automation"]["report.enabled"]; got.Source != "project" || got.Value != true {
		t.Errorf("values automation.report.enabled = %+v, want true from project", got)
	}
	if got := out.Layers["automation.report.enabled"]; len(got) != 1 || got["project"] != true {
		t.Errorf("layers[automation.report.enabled] = %v, want project true only", got)
	}
	if want := userPath + ": automation.report is not a table."; strings.Count(out.ConfigError, want) != 1 {
		t.Errorf("configError = %q, want it to contain %q once", out.ConfigError, want)
	}
}

// A repo with no config answers empty layers and empty lists, never null.
func TestDashboardSetupState_LayersAndGuardrailsEmpty(t *testing.T) {
	root, _ := dashSetupRoot(t)
	out := dashSetupState(t, root)
	data, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	if got := string(m["layers"]); got != "{}" {
		t.Errorf("layers = %s, want {}", got)
	}
	if got := string(m["guardrails"]); got != `{"execute.guardrails":[],"plan.guardrails":[]}` {
		t.Errorf("guardrails = %s, want two empty lists", got)
	}
}

// The guardrail lists are sorted by id and default the severity to error and
// the description to "". The execute list reads [execute.guardrails].
func TestDashboardSetupState_Guardrails(t *testing.T) {
	root, _ := dashSetupRoot(t)
	dashSetupConfig(t, root, "[plan.guardrails.zeta]\ndescription = \"Last.\"\nseverity = \"warning\"\n"+
		"[plan.guardrails.alpha]\ndescription = \"First.\"\n"+
		"[plan.guardrails.bare]\n"+
		"[execute.guardrails.no-skip]\ndescription = \"Do not skip tests.\"\nseverity = \"warning\"\n")
	out := dashSetupState(t, root)

	want := []DashboardGuardrail{
		{ID: "alpha", Severity: "error", Description: "First."},
		{ID: "bare", Severity: "error", Description: ""},
		{ID: "zeta", Severity: "warning", Description: "Last."},
	}
	got := out.Guardrails["plan.guardrails"]
	if len(got) != len(want) {
		t.Fatalf("plan.guardrails = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("plan.guardrails[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
	wantExecute := []DashboardGuardrail{{ID: "no-skip", Severity: "warning", Description: "Do not skip tests."}}
	if got := out.Guardrails["execute.guardrails"]; !slices.Equal(got, wantExecute) {
		t.Errorf("execute.guardrails = %+v, want %+v", got, wantExecute)
	}
}

// setupGuardrailList reads each form of the guardrails value. A skipped entry
// gives one note.
func TestSetupGuardrailList(t *testing.T) {
	const file = "/repo/.sdlc-v2/config.toml"
	for _, tc := range []struct {
		name    string
		section map[string]any
		want    []DashboardGuardrail
		notes   []string
	}{
		{"absent", map[string]any{}, []DashboardGuardrail{}, nil},
		{"text", map[string]any{"guardrails": "x"}, []DashboardGuardrail{}, nil},
		{"named tables skip other values", map[string]any{"guardrails": map[string]any{
			"b": map[string]any{"severity": "warning", "description": "B."}, "a": map[string]any{}, "note": "x"}},
			[]DashboardGuardrail{{"a", "error", ""}, {"b", "warning", "B."}},
			[]string{file + ": plan.guardrails.note is not a table. The page skips it."}},
		{"array of tables", map[string]any{"guardrails": []map[string]any{
			{"id": "b", "description": "B."}, {"id": "c"}, {"id": "a", "severity": "warning"}, {"description": "no id"}}},
			[]DashboardGuardrail{{"a", "warning", ""}, {"b", "error", "B."}, {"c", "error", ""}},
			[]string{file + ": plan.guardrails entry 4 has no text id. The page skips it."}},
		// The state shows a list in array form as it is: a repeated id gives
		// one entry for each table, in file order. The save refuses to edit
		// a list in array form.
		{"array of tables with a repeated id", map[string]any{"guardrails": []map[string]any{
			{"id": "a", "description": "First."}, {"id": "b"}, {"id": "a", "description": "Second."}}},
			[]DashboardGuardrail{{"a", "error", "First."}, {"a", "error", "Second."}, {"b", "error", ""}},
			nil},
		{"inline list", map[string]any{"guardrails": []any{map[string]any{"id": "a"}, "x", map[string]any{"id": 3}}},
			[]DashboardGuardrail{{"a", "error", ""}},
			[]string{
				file + ": plan.guardrails entry 2 is not a table. The page skips it.",
				file + ": plan.guardrails entry 3 has no text id. The page skips it.",
			}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var notes []string
			got := setupGuardrailList(tc.section, "plan.guardrails", file, func(s string) { notes = append(notes, s) })
			if got == nil || len(got) != len(tc.want) {
				t.Fatalf("list = %#v, want %#v", got, tc.want)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Errorf("list[%d] = %+v, want %+v", i, got[i], tc.want[i])
				}
			}
			if strings.Join(notes, "|") != strings.Join(tc.notes, "|") {
				t.Errorf("notes = %q, want %q", notes, tc.notes)
			}
		})
	}
}

// A severity or a description that is present and not text gives the
// default and one note that names the file, the path, the id and the field.
// An absent field gives no note.
func TestSetupGuardrailList_WrongType(t *testing.T) {
	const file = "/repo/.sdlc-v2/config.toml"
	section := map[string]any{"guardrails": map[string]any{
		"dry":  map[string]any{"severity": int64(3), "description": map[string]any{"text": "x"}},
		"bare": map[string]any{},
	}}
	var notes []string
	got := setupGuardrailList(section, "execute.guardrails", file, func(s string) { notes = append(notes, s) })

	want := []DashboardGuardrail{{"bare", "error", ""}, {"dry", "error", ""}}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("list = %+v, want %+v", got, want)
	}
	wantNotes := []string{
		file + `: execute.guardrails.dry severity is not a string. The page shows "error".`,
		file + `: execute.guardrails.dry description is not a string. The page shows "".`,
	}
	if strings.Join(notes, "|") != strings.Join(wantNotes, "|") {
		t.Errorf("notes = %q, want %q", notes, wantNotes)
	}
}

// A guardrail note reaches configError.
func TestDashboardSetupState_GuardrailNotes(t *testing.T) {
	root, _ := dashSetupRoot(t)
	cfg := filepath.Join(root, ".sdlc-v2", "config.toml")
	dashSetupConfig(t, root, "[plan.guardrails.dry]\nseverity = 3\n")
	out := dashSetupState(t, root)
	if want := cfg + `: plan.guardrails.dry severity is not a string. The page shows "error".`; !strings.Contains(out.ConfigError, want) {
		t.Errorf("configError = %q, want it to contain %q", out.ConfigError, want)
	}
	if got := out.Guardrails["plan.guardrails"]; len(got) != 1 || got[0] != (DashboardGuardrail{"dry", "error", ""}) {
		t.Errorf("plan.guardrails = %+v, want dry with severity error", got)
	}
}
