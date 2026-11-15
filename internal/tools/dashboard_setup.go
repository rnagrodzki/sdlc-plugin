// Package tools: field list and repo state of the dashboard Setup tab.
//
// DashboardSetupFields returns the full field list. DashboardSetupState reads
// the config files of one repo and returns the state of the repo, the value
// of each form field with its source, and the state of the delegated rows.
package tools

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rnagrodzki/sdlc-plugin/internal/config"
	"github.com/rnagrodzki/sdlc-plugin/internal/configmigrate"
	"github.com/rnagrodzki/sdlc-plugin/internal/dimensions"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
	"github.com/rnagrodzki/sdlc-plugin/internal/setupmeta"
)

const (
	SetupStateReady    = "ready"         // .sdlc-v2/config.toml exists
	SetupStateNoConfig = "no-config"     // no config.toml and no config.json
	SetupStateLegacy   = "legacy-config" // config.json with no config.toml
)

// DashboardSetupFieldsOut is the answer of DashboardSetupFields.
type DashboardSetupFieldsOut struct {
	Sections []sectionRow `json:"sections"`
}

// DashboardSetupValue is the value of one field and the file it comes from.
type DashboardSetupValue struct {
	Value  any    `json:"value"`  // the value as the file holds it
	Source string `json:"source"` // "config" | "project" | "user"
}

// DashboardSetupFiles holds the absolute path of each config file.
type DashboardSetupFiles struct {
	Config  string `json:"config"`  // absolute path of .sdlc-v2/config.toml
	Project string `json:"project"` // absolute path of .sdlc-v2/local.toml
	User    string `json:"user"`    // absolute path of the user file; "" when there is none
}

// DashboardDelegatedState is the state of one row that the form cannot edit.
type DashboardDelegatedState struct {
	ID    string `json:"id"` // setup section id
	Set   bool   `json:"set"`
	Count *int   `json:"count,omitempty"` // key count or item count; absent for a section with neither
	Mode  string `json:"mode,omitempty"`  // pr-labels only: the key mode of [pr.labels]; "" when absent
}

// DashboardSetupStateOut is the answer of DashboardSetupState.
type DashboardSetupStateOut struct {
	Root        string                                    `json:"root"`
	State       string                                    `json:"state"`
	Files       DashboardSetupFiles                       `json:"files"`
	ConfigError string                                    `json:"configError"` // "" when config.Read gives no error
	Values      map[string]map[string]DashboardSetupValue `json:"values"`      // form section id -> field name
	Delegated   []DashboardDelegatedState                 `json:"delegated"`
}

// DashboardSetupFields returns the full field list: every section of
// setupmeta.Sections(), with the details and the effects of each field.
func DashboardSetupFields() DashboardSetupFieldsOut {
	return DashboardSetupFieldsOut{Sections: setupSectionRows(true)}
}

// dashboardSetupSuggestion is the Suggestion of an error that a read of the
// repo files returns.
const dashboardSetupSuggestion = "Check that .sdlc-v2 is readable, then reload the tab."

// setupRepoState returns the state of root from the files on disk.
func setupRepoState(root string) (string, error) {
	configPath := filepath.Join(root, paths.DataDir, paths.ConfigFile)
	_, err := os.Stat(configPath)
	if err == nil {
		return SetupStateReady, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", readFileError("dashboard setup: stat", configPath, err)
	}
	if errors.Is(configmigrate.Verify(root), configmigrate.ErrVersionStale) {
		return SetupStateLegacy, nil
	}
	return SetupStateNoConfig, nil
}

// DashboardSetupState reads the config files of root and returns the state of
// the repo, the value of each form field, and the state of each delegated
// row. root is the main worktree of the repo.
func DashboardSetupState(root string) (DashboardSetupStateOut, error) {
	state, err := setupRepoState(root)
	if err != nil {
		return DashboardSetupStateOut{}, err
	}
	out := DashboardSetupStateOut{
		Root:      root,
		State:     state,
		Files:     DashboardSetupFiles{Config: filepath.Join(root, paths.DataDir, paths.ConfigFile), Project: filepath.Join(root, paths.DataDir, paths.LocalConfigFile)},
		Values:    map[string]map[string]DashboardSetupValue{},
		Delegated: []DashboardDelegatedState{},
	}
	if p, ok := config.UserConfigPath(); ok {
		out.Files.User = p
	}

	configDoc, err := readSetupFile(out.Files.Config)
	if err != nil {
		return DashboardSetupStateOut{}, err
	}
	projectDoc, err := readSetupFile(out.Files.Project)
	if err != nil {
		return DashboardSetupStateOut{}, err
	}
	var userDoc map[string]any
	if out.Files.User != "" {
		if userDoc, err = readSetupFile(out.Files.User); err != nil {
			return DashboardSetupStateOut{}, err
		}
	}

	var notes []string
	addNote := func(s string) {
		for _, n := range notes {
			if n == s {
				return
			}
		}
		notes = append(notes, s)
	}
	for _, sec := range setupmeta.Sections() {
		if sec.ConfigPath == "" || len(sec.Fields) == 0 {
			continue
		}
		out.Values[sec.ID] = setupSectionValues(sec, out.Files, configDoc, projectDoc, userDoc, addNote)
	}

	if state == SetupStateReady {
		if _, err := config.Read(root); err != nil {
			out.ConfigError = err.Error()
		}
		if out.Delegated, err = dashboardDelegated(root, configDoc); err != nil {
			return DashboardSetupStateOut{}, err
		}
	}
	if len(notes) > 0 {
		if out.ConfigError != "" {
			notes = append([]string{out.ConfigError}, notes...)
		}
		out.ConfigError = strings.Join(notes, " ")
	}
	return out, nil
}

// setupSectionValues returns the value of each field of the form section sec.
// A section of config.toml reads the file config.toml. A section of
// local.toml reads the project file first, then the user file, so the
// project file wins. addNote receives the sentence for a value that is not a
// table where the field list needs a table.
func setupSectionValues(sec setupmeta.Section, files DashboardSetupFiles,
	configDoc, projectDoc, userDoc map[string]any, addNote func(string)) map[string]DashboardSetupValue {
	type layer struct {
		source string
		file   string
		doc    map[string]any
	}
	layers := []layer{{"project", files.Project, projectDoc}, {"user", files.User, userDoc}}
	top, _, _ := strings.Cut(sec.ConfigPath, ".")
	if config.ProjectSections[top] {
		layers = []layer{{"config", files.Config, configDoc}}
	}

	values := map[string]DashboardSetupValue{}
	for _, l := range layers {
		if l.doc == nil {
			continue
		}
		table, ok := setupTable(l.doc, sec.ConfigPath)
		if !ok {
			addNote(fmt.Sprintf("%s: %s is not a table.", l.file, sec.ConfigPath))
			continue
		}
		for _, f := range sec.Fields {
			if _, set := values[f.Name]; set {
				continue
			}
			value, present, badParent := setupFieldValue(table, f.Name)
			if badParent != "" {
				addNote(fmt.Sprintf("%s: %s.%s is not a table.", l.file, sec.ConfigPath, badParent))
				continue
			}
			if present {
				values[f.Name] = DashboardSetupValue{Value: value, Source: l.source}
			}
		}
	}
	return values
}

// setupFieldValue returns the value of the field name in table. A dotted name
// reads the nested value. When a parent of the name holds a value that is not
// a table, badParent is the dotted path of that parent, relative to table.
func setupFieldValue(table map[string]any, name string) (value any, present bool, badParent string) {
	parts := strings.Split(name, ".")
	cur := table
	for i, part := range parts {
		next, ok := cur[part]
		if !ok {
			return nil, false, ""
		}
		if i == len(parts)-1 {
			return next, true, ""
		}
		child, isTable := next.(map[string]any)
		if !isTable {
			return nil, false, strings.Join(parts[:i+1], ".")
		}
		cur = child
	}
	return nil, false, ""
}

// dashboardDelegated returns the state of each row that the form cannot edit,
// in the order of setupmeta.Sections().
func dashboardDelegated(root string, configDoc map[string]any) ([]DashboardDelegatedState, error) {
	rows := []DashboardDelegatedState{}
	for _, sec := range setupmeta.Sections() {
		if sec.DelegatedTo == "" {
			continue
		}
		row := DashboardDelegatedState{ID: sec.ID}
		count := func(n int) {
			row.Count = &n
			row.Set = n > 0
		}
		switch sec.ID {
		case "commit":
			table, _ := setupTable(configDoc, "commit")
			count(len(table))
		case "pr":
			table, _ := setupTable(configDoc, "pr")
			n := len(table)
			if _, hasLabels := table["labels"]; hasLabels {
				n--
			}
			count(n)
		case "pr-labels":
			table, _ := setupTable(configDoc, "pr.labels")
			row.Set = len(table) > 0
			if mode, ok := table["mode"].(string); ok {
				row.Mode = mode
			}
		case "review-dimensions":
			loaded, err := dimensions.Load(filepath.Join(root, paths.DataDir, paths.ReviewDimensionsSubdir))
			if err != nil {
				return nil, dashboardSetupInfraError("list the review dimensions", err)
			}
			count(len(loaded))
		case "pr-template", "plan-template":
			name := paths.PRTemplateFile
			if sec.ID == "plan-template" {
				name = paths.PlanTemplateFile
			}
			res, err := setupCheckTemplateExists(root, name)
			if err != nil {
				return nil, dashboardSetupInfraError("check the template "+name, err)
			}
			row.Set = res.Exists
		case "plan-guardrails":
			count(setupListLen(configDoc, "plan", "guardrails"))
		case "execution-guardrails":
			count(setupListLen(configDoc, "execute", "guardrails"))
		case "openspec-block":
			set, err := openspecBlockSet(root)
			if err != nil {
				return nil, err
			}
			row.Set = set
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// setupListLen returns the number of entries of the list at table.key in
// doc. An absent key, or a value that is not a list, gives 0.
func setupListLen(doc map[string]any, table, key string) int {
	t, _ := setupTable(doc, table)
	switch list := t[key].(type) {
	case []any:
		return len(list)
	case []map[string]any:
		return len(list)
	case []string:
		return len(list)
	}
	return 0
}

// openspecBlockSet reports whether openspec/config.yaml holds the managed
// block of /setup. An absent file gives false.
func openspecBlockSet(root string) (bool, error) {
	path := filepath.Join(root, "openspec", "config.yaml")
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, readFileError("dashboard setup: stat", path, err)
	}
	if info.Size() > maxCheckedFileBytes {
		return false, &mcpserver.DataError{
			Msg:        fmt.Sprintf("dashboard setup: %s is %d bytes, over the %d byte limit", path, info.Size(), maxCheckedFileBytes),
			Suggestion: fmt.Sprintf("Reduce %s to %d bytes or less, then reload the tab.", path, maxCheckedFileBytes),
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false, readFileError("dashboard setup: read", path, err)
	}
	return enrichBeginRe.Match(data), nil
}

// dashboardSetupInfraError wraps a failed read of a delegated row.
func dashboardSetupInfraError(what string, cause error) error {
	return &mcpserver.InfraError{
		Msg:        fmt.Sprintf("dashboard setup: %s: %s", what, cause.Error()),
		Suggestion: dashboardSetupSuggestion,
		Cause:      cause,
	}
}
