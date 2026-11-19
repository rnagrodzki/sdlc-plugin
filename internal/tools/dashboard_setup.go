// Package tools: field list and repo state of the dashboard Setup tab.
//
// DashboardSetupFields returns the full field list. DashboardSetupState reads
// the config files of one repo and returns the state of the repo, the value
// of each form field with its source, and the state of the delegated rows.

package tools

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/rnagrodzki/sdlc-plugin/internal/config"
	"github.com/rnagrodzki/sdlc-plugin/internal/configmigrate"
	"github.com/rnagrodzki/sdlc-plugin/internal/dimensions"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
	"github.com/rnagrodzki/sdlc-plugin/internal/setupmeta"
)

// The states of a repo for the Setup tab. setupRepoState returns one of
// them, and the save and the create allow only some of them.
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
	Source string `json:"source"` // setupTargetConfig | setupTargetProject | setupTargetUser
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

// DashboardGuardrail is one guardrail of a plan or execute guardrail list.
type DashboardGuardrail struct {
	ID          string `json:"id"`
	Severity    string `json:"severity"` // "error" when the table has no severity
	Description string `json:"description"`
}

// setupGuardrailDefaultSeverity is the severity of a guardrail with no severity.
const setupGuardrailDefaultSeverity = "error"

// DashboardSetupStateOut is the answer of DashboardSetupState.
type DashboardSetupStateOut struct {
	Root        string                                    `json:"root"`
	State       string                                    `json:"state"`
	Files       DashboardSetupFiles                       `json:"files"`
	ConfigError string                                    `json:"configError"` // "" when config.Read gives no error
	Values      map[string]map[string]DashboardSetupValue `json:"values"`      // form section id -> field name
	Delegated   []DashboardDelegatedState                 `json:"delegated"`
	// Layers maps "<section id>.<field>" to the value of each file that sets
	// the field. The keys of the inner map are setupTargetConfig,
	// setupTargetProject and setupTargetUser.
	Layers map[string]map[string]any `json:"layers"`
	// Guardrails maps "plan.guardrails" and "execute.guardrails" to the
	// guardrails of config.toml, sorted by id. Each list is [] when empty.
	Guardrails map[string][]DashboardGuardrail `json:"guardrails"`
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
		return "", dashboardSetupReadError("stat", configPath, err)
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
		Delegated: []DashboardDelegatedState{},
	}
	if p, ok := config.UserConfigPath(); ok {
		out.Files.User = p
	}

	configDoc, err := readCheckedTOML(out.Files.Config, setupStateReader)
	if err != nil {
		return DashboardSetupStateOut{}, err
	}
	projectDoc, err := readCheckedTOML(out.Files.Project, setupStateReader)
	if err != nil {
		return DashboardSetupStateOut{}, err
	}
	var userDoc map[string]any
	if out.Files.User != "" {
		if userDoc, err = readCheckedTOML(out.Files.User, setupStateReader); err != nil {
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
	out.Values, out.Layers = setupFormValues(out.Files, configDoc, projectDoc, userDoc, addNote)

	var guardrailDoc map[string]any // nil gives [] for each guardrail list
	if state == SetupStateReady {
		if _, err := config.Read(root); err != nil {
			out.ConfigError = err.Error()
		}
		if out.Delegated, err = dashboardDelegated(root, out.Files.Config, configDoc, addNote); err != nil {
			return DashboardSetupStateOut{}, err
		}
		guardrailDoc = configDoc
	}
	out.Guardrails = setupStateGuardrails(guardrailDoc, out.Files.Config, addNote)
	if len(notes) > 0 {
		if out.ConfigError != "" {
			notes = append([]string{out.ConfigError}, notes...)
		}
		out.ConfigError = strings.Join(notes, " ")
	}
	return out, nil
}

// setupFormValues returns the value of each field of each form section, by
// form section id, and the layers of each field (setupSectionValues).
func setupFormValues(files DashboardSetupFiles, configDoc, projectDoc, userDoc map[string]any,
	addNote func(string)) (map[string]map[string]DashboardSetupValue, map[string]map[string]any) {
	values := map[string]map[string]DashboardSetupValue{}
	fieldLayers := map[string]map[string]any{}
	for _, sec := range setupmeta.Sections() {
		if sec.ConfigPath == "" || len(sec.Fields) == 0 {
			continue
		}
		values[sec.ID] = setupSectionValues(sec, files, configDoc, projectDoc, userDoc, addNote, fieldLayers)
	}
	return values, fieldLayers
}

// setupStateGuardrails returns the guardrail lists of configDoc, keyed by the
// config path of each guardrail section, in setupmeta.Sections() order. A
// list is [] when its parent table is absent or is not a table.
// dashboardDelegated already notes a parent value that is not a table, and a
// guardrails value that is not a list or a table.
func setupStateGuardrails(configDoc map[string]any, configFile string, addNote func(string)) map[string][]DashboardGuardrail {
	out := map[string][]DashboardGuardrail{}
	for _, sec := range setupGuardrailSections() {
		out[sec.ConfigPath] = []DashboardGuardrail{}
		parent, _, _ := strings.Cut(sec.ConfigPath, ".")
		if table, ok := setupTable(configDoc, parent); ok {
			out[sec.ConfigPath] = setupGuardrailList(table, sec.ConfigPath, configFile, addNote)
		}
	}
	return out
}

// setupSectionValues returns the value of each field of the form section sec.
// A section of config.toml reads the file config.toml. A section of
// local.toml reads the project file first, then the user file, so the
// project file wins. addNote receives the sentence for a value that is not a
// table where the field list needs a table, also in a file that another file
// overrides. fieldLayers receives, under the key "<section id>.<field name>",
// the value of each file that sets the field, including the files that
// another file overrides.
func setupSectionValues(sec setupmeta.Section, files DashboardSetupFiles,
	configDoc, projectDoc, userDoc map[string]any, addNote func(string),
	fieldLayers map[string]map[string]any) map[string]DashboardSetupValue {
	type layer struct {
		source string
		file   string
		doc    map[string]any
	}
	layers := []layer{{setupTargetProject, files.Project, projectDoc}, {setupTargetUser, files.User, userDoc}}
	top, _, _ := strings.Cut(sec.ConfigPath, ".")
	if config.ProjectSections[top] {
		layers = []layer{{setupTargetConfig, files.Config, configDoc}}
	}

	addLayer := func(k, source string, v any) {
		if fieldLayers[k] == nil {
			fieldLayers[k] = map[string]any{}
		}
		fieldLayers[k][source] = v
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
			value, present, badParent := setupFieldValue(table, f.Name)
			if badParent != "" {
				addNote(fmt.Sprintf("%s: %s.%s is not a table.", l.file, sec.ConfigPath, badParent))
				continue
			}
			if !present {
				continue
			}
			addLayer(sec.ID+"."+f.Name, l.source, value)
			if _, set := values[f.Name]; !set {
				values[f.Name] = DashboardSetupValue{Value: value, Source: l.source}
			}
		}
	}
	return values
}

// setupGuardrailList returns the guardrails in the guardrails key of section,
// sorted by id. The named-table form ([plan.guardrails.<id>]) gives one entry
// for each table. The array form ([[plan.guardrails]]) gives one entry for
// each table with a text id. An entry with no severity gets the severity
// "error", and an entry with no description gets "". A severity or a
// description that is not text gets the same default, and addNote receives a
// sentence that names path, the id and the field in configFile. An entry that
// is not a table, or an array entry with no text id, is skipped, and addNote
// receives a sentence for it. An absent key or a value of another type gives
// [] with no note; setupGuardrailCount notes the value of another type.
func setupGuardrailList(section map[string]any, path, configFile string, addNote func(string)) []DashboardGuardrail {
	list := []DashboardGuardrail{}
	text := func(id string, table map[string]any, field, def string) string {
		raw, present := table[field]
		if !present {
			return def
		}
		s, ok := raw.(string)
		if !ok {
			addNote(fmt.Sprintf("%s: %s.%s %s is not a string. The page shows %q.", configFile, path, id, field, def))
			return def
		}
		return s
	}
	entry := func(id string, table map[string]any) {
		list = append(list, DashboardGuardrail{
			ID:          id,
			Severity:    text(id, table, "severity", setupGuardrailDefaultSeverity),
			Description: text(id, table, "description", ""),
		})
	}
	arrayEntry := func(i int, v any) {
		table, ok := v.(map[string]any)
		if !ok {
			addNote(fmt.Sprintf("%s: %s entry %d is not a table. The page skips it.", configFile, path, i+1))
			return
		}
		id, ok := table["id"].(string)
		if !ok || id == "" {
			addNote(fmt.Sprintf("%s: %s entry %d has no text id. The page skips it.", configFile, path, i+1))
			return
		}
		entry(id, table)
	}
	switch raw := section["guardrails"].(type) {
	case map[string]any:
		for _, id := range slices.Sorted(maps.Keys(raw)) {
			table, ok := raw[id].(map[string]any)
			if !ok {
				addNote(fmt.Sprintf("%s: %s.%s is not a table. The page skips it.", configFile, path, id))
				continue
			}
			entry(id, table)
		}
	case []map[string]any:
		for i, table := range raw {
			arrayEntry(i, table)
		}
	case []any:
		for i, v := range raw {
			arrayEntry(i, v)
		}
	}
	slices.SortStableFunc(list, func(a, b DashboardGuardrail) int { return strings.Compare(a.ID, b.ID) })
	return list
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
// in the order of setupmeta.Sections(). configFile is the path of
// config.toml. addNote receives the sentence for a value of config.toml that
// does not have the type that a row reads; the row then counts that value as
// not set.
func dashboardDelegated(root, configFile string, configDoc map[string]any, addNote func(string)) ([]DashboardDelegatedState, error) {
	table := func(key string) map[string]any {
		t, ok := setupTable(configDoc, key)
		if !ok {
			addNote(fmt.Sprintf("%s: %s is not a table.", configFile, key))
		}
		return t
	}
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
			count(len(table("commit")))
		case "pr":
			pr := table("pr")
			n := len(pr)
			if _, hasLabels := pr["labels"]; hasLabels {
				n--
			}
			count(n)
		case "pr-labels":
			labels := table("pr.labels")
			row.Set = len(labels) > 0
			if raw, present := labels["mode"]; present {
				if mode, ok := raw.(string); ok {
					row.Mode = mode
				} else {
					addNote(fmt.Sprintf("%s: pr.labels.mode is not a text.", configFile))
				}
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
		case setupmeta.PlanGuardrailsID:
			count(setupGuardrailCount(table("plan"), sec.ConfigPath, configFile, addNote))
		case setupmeta.ExecutionGuardrailsID:
			count(setupGuardrailCount(table("execute"), sec.ConfigPath, configFile, addNote))
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

// setupGuardrailCount returns the number of guardrails in the guardrails key
// of section. It counts each entry of the array-of-tables form
// ([[plan.guardrails]]) and each table of the named-table form
// ([plan.guardrails.<id>]). An absent key gives 0. A value of another type
// gives 0, and addNote receives a sentence that names path in configFile.
func setupGuardrailCount(section map[string]any, path, configFile string, addNote func(string)) int {
	raw, present := section["guardrails"]
	if !present {
		return 0
	}
	switch list := raw.(type) {
	case []any:
		return len(list)
	case []map[string]any:
		return len(list)
	case map[string]any:
		n := 0
		for _, entry := range list {
			if _, isTable := entry.(map[string]any); isTable {
				n++
			}
		}
		return n
	}
	addNote(fmt.Sprintf("%s: %s is not a list or a table.", configFile, path))
	return 0
}

// openspecBlockSet reports whether openspec/config.yaml holds the managed
// block of /setup. An absent file gives false.
func openspecBlockSet(root string) (bool, error) {
	path := openspecConfigPath(root)
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, dashboardSetupReadError("stat", path, err)
	}
	if info.Size() > maxCheckedFileBytes {
		return false, &mcpserver.DataError{
			Msg:        fmt.Sprintf("dashboard setup: %s is %d bytes, over the %d byte limit", path, info.Size(), maxCheckedFileBytes),
			Suggestion: fmt.Sprintf("Reduce %s to %d bytes or less, then reload the tab.", path, maxCheckedFileBytes),
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false, dashboardSetupReadError("read", path, err)
	}
	return enrichBeginRe.Match(data), nil
}

// dashboardSetupReadError is the InfraError of a failed stat or read of path
// by the Setup tab. op is "stat" or "read". Its Suggestion does not say that
// nothing was written, because the state read writes nothing.
func dashboardSetupReadError(op, path string, cause error) error {
	return &mcpserver.InfraError{
		Msg:        fmt.Sprintf("dashboard setup: %s %s: %s", op, path, cause.Error()),
		Suggestion: fmt.Sprintf("Check that %s is readable, then reload the tab.", path),
		Cause:      cause,
	}
}

// dashboardSetupInfraError wraps a failed read of a delegated row.
func dashboardSetupInfraError(what string, cause error) error {
	return &mcpserver.InfraError{
		Msg:        fmt.Sprintf("dashboard setup: %s: %s", what, cause.Error()),
		Suggestion: dashboardSetupSuggestion,
		Cause:      cause,
	}
}
