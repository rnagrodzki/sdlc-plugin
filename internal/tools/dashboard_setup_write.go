// Package tools: Save and Create of the dashboard Setup tab.
//
// DashboardSetupSave applies the changed fields of the Setup form to the
// config files of one repo. The write code (setupWriteSections) replaces a
// whole section, so the save reads each changed section from its target file,
// applies the changes to a copy, and sends the complete section. Keys that the
// form does not show stay in the file. DashboardSetupCreate writes the first
// config of a repo through the init code of the setup tool.

package tools

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/rnagrodzki/sdlc-plugin/internal/config"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/setupmeta"
)

// maxSetupChanges is the largest number of entries in one request. The
// count rule runs before each other request rule. The field list has 48
// form fields. A valid save holds at most 48 changes.
const maxSetupChanges = 64

// DashboardSetupChange is one changed field of the Setup form.
type DashboardSetupChange struct {
	Section string `json:"section"`         // form section id, for example "plan-tasks"
	Field   string `json:"field"`           // field name of that section
	Op      string `json:"op"`              // "set" | "reset"
	Value   any    `json:"value,omitempty"` // required with set; refused with reset
}

// DashboardSetupSaveIn is the request of a save.
type DashboardSetupSaveIn struct {
	Changes []DashboardSetupChange `json:"changes"`
	Targets map[string]string      `json:"targets"` // form section id of a local section -> "project" | "user"
}

// DashboardSetupWrite is one section that a save wrote.
type DashboardSetupWrite struct {
	Section string `json:"section"` // form section id
	File    string `json:"file"`    // absolute path
}

// DashboardSetupFailure is one section that a save did not write.
type DashboardSetupFailure struct {
	Section string `json:"section"` // form section id
	File    string `json:"file"`
	Message string `json:"message"`
}

// DashboardSetupSaveOut is the result of a save.
type DashboardSetupSaveOut struct {
	OK       bool                    `json:"ok"`
	Written  []DashboardSetupWrite   `json:"written"`  // [] when none
	Failed   []DashboardSetupFailure `json:"failed"`   // [] when none
	Warnings []string                `json:"warnings"` // [] when none
	CIFiles  []string                `json:"ciFiles"`  // CI files that the save created or updated; [] when none
}

// DashboardSetupCreateOut is the result of a create.
type DashboardSetupCreateOut struct {
	OK      bool     `json:"ok"`
	Created []string `json:"created"` // [] when none
	Changed []string `json:"changed"` // [] when none
	Errors  []string `json:"errors"`  // [] when none
}

// DashboardSetupInvalid is the error of a save with a refused value.
type DashboardSetupInvalid struct{ Fields []SetupFieldError }

// Error states the count of the refused values, then the message of each one.
func (e *DashboardSetupInvalid) Error() string {
	noun := "field values are"
	if len(e.Fields) == 1 {
		noun = "field value is"
	}
	parts := make([]string, 0, len(e.Fields))
	for _, f := range e.Fields {
		parts = append(parts, f.Path+": "+f.Message)
	}
	return fmt.Sprintf("dashboard setup save: %d %s refused. %s", len(e.Fields), noun, strings.Join(parts, " "))
}

// DashboardSetupNotReady is the error of a save or a create in a repo
// whose state does not allow it.
type DashboardSetupNotReady struct{ State, Suggestion string }

// Error names the state. The Suggestion text stays in its own field.
func (e *DashboardSetupNotReady) Error() string {
	return fmt.Sprintf("dashboard setup: the repo state %s does not allow this change", e.State)
}

// setupWriteFunc is the write call of the save. A test replaces it to make
// the second write fail (pattern: internal/tools/fsseam.go:18-25).
var setupWriteFunc = setupWriteSections

// Suggestion texts of the save and the create.
const (
	dashboardSetupRequestSuggestion   = "The page sent a request that breaks this rule. Reload the Setup tab, then save again."
	dashboardSetupNoConfigSuggestion  = "Select Create config on the Setup tab, or run /sdlc:setup in the repo. Then save again."
	dashboardSetupLegacySuggestion    = "Run /sdlc:setup --migrate in the repo. Then save again."
	dashboardSetupHasConfigSuggestion = "The repo already has a config. Reload the Setup tab."
)

// setupSaveSection is one changed form section of a save.
type setupSaveSection struct {
	sec     setupmeta.Section
	key     string                 // config path of the section
	target  string                 // "config" | "project" | "user"
	changes []DashboardSetupChange // the changes of this section, in request order
	stored  map[string]any         // the table that the target file holds now
	next    map[string]any         // stored, with each change applied
	// project is, for a user target only, a copy of the table of the
	// project file without each changed key. projectRemoved is true when
	// that copy lost a key.
	project        map[string]any
	projectRemoved bool
}

// DashboardSetupSave applies the changes of in to the config files of root.
// root is the main worktree of the repo. Request, state and value errors
// change no file.
func DashboardSetupSave(root string, in DashboardSetupSaveIn) (DashboardSetupSaveOut, error) {
	if err := checkSetupSaveState(root); err != nil {
		return DashboardSetupSaveOut{}, err
	}
	sections, userPath, err := checkSetupSaveRequest(in)
	if err != nil {
		return DashboardSetupSaveOut{}, err
	}
	if err := readSetupSaveSections(root, userPath, sections); err != nil {
		return DashboardSetupSaveOut{}, err
	}
	if err := checkSetupSaveValues(sections); err != nil {
		return DashboardSetupSaveOut{}, err
	}
	return writeSetupSave(root, userPath, sections)
}

// checkSetupSaveState refuses a save in a repo that has no config or a
// legacy config.
func checkSetupSaveState(root string) error {
	state, err := setupRepoState(root)
	if err != nil {
		return err
	}
	switch state {
	case SetupStateNoConfig:
		return &DashboardSetupNotReady{State: state, Suggestion: dashboardSetupNoConfigSuggestion}
	case SetupStateLegacy:
		return &DashboardSetupNotReady{State: state, Suggestion: dashboardSetupLegacySuggestion}
	}
	return nil
}

// setupDocCache reads each config file one time. The key is the absolute path.
type setupDocCache map[string]map[string]any

// read returns the decoded file at path.
func (c setupDocCache) read(path string) (map[string]any, error) {
	if doc, ok := c[path]; ok {
		return doc, nil
	}
	doc, err := readSetupFile(path)
	if err != nil {
		return nil, err
	}
	c[path] = doc
	return doc, nil
}

// readSetupSaveSections reads each target file one time. For each section it
// sets stored and next, and for a user target also project.
func readSetupSaveSections(root, userPath string, sections []*setupSaveSection) error {
	docs := setupDocCache{}
	for _, s := range sections {
		if err := readSetupSaveSection(root, userPath, s, docs); err != nil {
			return err
		}
	}
	return nil
}

// readSetupSaveSection reads the table of one section from its target file
// and applies the changes to a copy of it.
func readSetupSaveSection(root, userPath string, s *setupSaveSection, docs setupDocCache) error {
	file := setupSavePath(root, userPath, s.target, s.key)
	doc, err := docs.read(file)
	if err != nil {
		return err
	}
	stored, ok := setupTable(doc, s.key)
	if !ok {
		return setupSaveNotTableError(s.key, file)
	}
	s.stored = stored
	s.next = setupDeepCopy(stored).(map[string]any)
	for _, c := range s.changes {
		bad, _ := setupApplyChange(s.next, c.Field, c.Op, c.Value)
		if bad != "" {
			return setupSaveNotTableError(s.key+"."+bad, file)
		}
	}
	if s.target != "user" {
		return nil
	}
	return readSetupSaveProject(root, userPath, s, docs)
}

// readSetupSaveProject sets project of a user-target section: a copy of the
// table of the project file without each changed key.
func readSetupSaveProject(root, userPath string, s *setupSaveSection, docs setupDocCache) error {
	projectFile := setupSavePath(root, userPath, "project", s.key)
	projectDoc, err := docs.read(projectFile)
	if err != nil {
		return err
	}
	projectStored, ok := setupTable(projectDoc, s.key)
	if !ok {
		return setupSaveNotTableError(s.key, projectFile)
	}
	s.project = setupDeepCopy(projectStored).(map[string]any)
	for _, c := range s.changes {
		bad, removed := setupApplyChange(s.project, c.Field, "reset", nil)
		if bad != "" {
			return setupSaveNotTableError(s.key+"."+bad, projectFile)
		}
		s.projectRemoved = s.projectRemoved || removed
	}
	return nil
}

// checkSetupSaveValues turns flag lists to tables, then checks the values of
// each new section.
func checkSetupSaveValues(sections []*setupSaveSection) error {
	newSections := map[string]map[string]any{}
	storedByKey := map[string]map[string]any{}
	for _, s := range sections {
		if err := normalizeFlagSetFields(s.key, s.next); err != nil {
			return err
		}
		newSections[s.key] = s.next
		storedByKey[s.key] = s.stored
	}
	fieldErrs, err := checkSetupValues(newSections, func(key string) (map[string]any, error) {
		return storedByKey[key], nil
	})
	if err != nil {
		return err
	}
	if len(fieldErrs) > 0 {
		return &DashboardSetupInvalid{Fields: fieldErrs}
	}
	return nil
}

// writeSetupSave writes the user file first, then the config.toml sections
// and the project sections, and builds the result.
func writeSetupSave(root, userPath string, sections []*setupSaveSection) (DashboardSetupSaveOut, error) {
	out := DashboardSetupSaveOut{
		Written:  []DashboardSetupWrite{},
		Failed:   []DashboardSetupFailure{},
		Warnings: []string{},
		CIFiles:  []string{},
	}
	byKey := map[string]*setupSaveSection{}
	for _, s := range sections {
		byKey[s.key] = s
	}
	userWritten, userRan, err := writeSetupSaveUser(root, userPath, sections, byKey, &out)
	if err != nil {
		return DashboardSetupSaveOut{}, err
	}
	if err := writeSetupSaveProject(root, userPath, sections, byKey, userWritten, userRan, &out); err != nil {
		return DashboardSetupSaveOut{}, err
	}
	out.OK = len(out.Failed) == 0
	return out, nil
}

// writeSetupSaveUser writes the sections with the target user. It returns the
// keys that the write wrote, and whether a write call ran.
func writeSetupSaveUser(root, userPath string, sections []*setupSaveSection, byKey map[string]*setupSaveSection, out *DashboardSetupSaveOut) (map[string]bool, bool, error) {
	userSections := map[string]map[string]any{}
	for _, s := range sections {
		if s.target == "user" {
			userSections[s.key] = s.next
		}
	}
	userWritten := map[string]bool{}
	if len(userSections) == 0 {
		return userWritten, false, nil
	}
	res, err := setupSaveCall(root, userSections, "user")
	if err != nil {
		return nil, false, err
	}
	for _, key := range res.Written {
		userWritten[key] = true
	}
	setupSaveCollect(out, root, userPath, "user", userSections, res, byKey)
	return userWritten, true, nil
}

// writeSetupSaveProject writes the config.toml sections and the project
// sections. A project section of a user-target section is written only when
// the user write wrote its key and the project copy lost a key. When this
// write fails after the user write ran, the failure goes to out.Failed. When
// no user write ran, the failure is the error of the save.
func writeSetupSaveProject(root, userPath string, sections []*setupSaveSection, byKey map[string]*setupSaveSection, userWritten map[string]bool, userRan bool, out *DashboardSetupSaveOut) error {
	projectSections := map[string]map[string]any{}
	for _, s := range sections {
		switch {
		case s.target != "user":
			projectSections[s.key] = s.next
		case s.projectRemoved && userWritten[s.key]:
			projectSections[s.key] = s.project
		}
	}
	if len(projectSections) == 0 {
		return nil
	}
	res, err := setupSaveCall(root, projectSections, "")
	if err != nil {
		if !userRan {
			return err
		}
		for _, key := range sortedSetupKeys(projectSections) {
			out.Failed = append(out.Failed, DashboardSetupFailure{
				Section: byKey[key].sec.ID,
				File:    setupTargetPath(root, root, userPath, "", key),
				Message: err.Error(),
			})
		}
		return nil
	}
	setupSaveCollect(out, root, userPath, "", projectSections, res, byKey)
	for _, rep := range res.Scaffold {
		switch rep.Action {
		case "created", "overwritten", "migrated":
			out.CIFiles = append(out.CIFiles, rep.Path)
		}
	}
	return nil
}

// setupFormIndex lists the sections that the form can change.
type setupFormIndex struct {
	sections  map[string]setupmeta.Section // form section id -> section
	delegated map[string]bool              // ids of delegated sections
	order     []string                     // form section ids, in setupmeta.Sections() order
}

// newSetupFormIndex builds the index from setupmeta.Sections().
func newSetupFormIndex() setupFormIndex {
	idx := setupFormIndex{sections: map[string]setupmeta.Section{}, delegated: map[string]bool{}}
	for _, sec := range setupmeta.Sections() {
		if sec.DelegatedTo != "" {
			idx.delegated[sec.ID] = true
			continue
		}
		if sec.ConfigPath == "" || len(sec.Fields) == 0 {
			continue
		}
		idx.sections[sec.ID] = sec
		idx.order = append(idx.order, sec.ID)
	}
	return idx
}

// checkSetupSaveRequest checks the request rules of a save. It returns the
// changed sections in the order of setupmeta.Sections(), each with its config
// path and target, and the user file path ("" when no section has the target
// user).
func checkSetupSaveRequest(in DashboardSetupSaveIn) ([]*setupSaveSection, string, error) {
	if len(in.Changes) > maxSetupChanges {
		return nil, "", setupRequestError(fmt.Sprintf("changes has %d entries, over the limit of %d", len(in.Changes), maxSetupChanges))
	}
	if len(in.Changes) == 0 {
		return nil, "", setupRequestError("changes is empty")
	}
	idx := newSetupFormIndex()
	byID, err := groupSetupChanges(in.Changes, idx)
	if err != nil {
		return nil, "", err
	}
	if err := checkSetupTargets(in.Targets, byID); err != nil {
		return nil, "", err
	}
	out, needUser := resolveSetupTargets(in.Targets, idx.order, byID)
	if !needUser {
		return out, "", nil
	}
	userPath, ok := config.UserConfigPath()
	if !ok {
		return nil, "", &mcpserver.DomainError{
			Msg:        fmt.Sprintf("dashboard setup save: target user needs a user file path, and there is none (%s unset, no home directory found)", config.UserConfigPathEnv),
			Suggestion: fmt.Sprintf("Set %s to a file path, or save to This project.", config.UserConfigPathEnv),
		}
	}
	return out, userPath, nil
}

// groupSetupChanges checks each change and groups the changes by form
// section id, in request order.
func groupSetupChanges(changes []DashboardSetupChange, idx setupFormIndex) (map[string]*setupSaveSection, error) {
	byID := map[string]*setupSaveSection{}
	seen := map[string]bool{}
	for _, c := range changes {
		sec, err := checkSetupChange(c, idx, seen)
		if err != nil {
			return nil, err
		}
		s := byID[c.Section]
		if s == nil {
			s = &setupSaveSection{sec: sec, key: sec.ConfigPath}
			byID[c.Section] = s
		}
		s.changes = append(s.changes, c)
	}
	return byID, nil
}

// checkSetupChange checks the section, the field, the repeat, and the op with
// its value of one change, in this order. It records the change in seen and
// returns the section of the change.
func checkSetupChange(c DashboardSetupChange, idx setupFormIndex, seen map[string]bool) (setupmeta.Section, error) {
	sec, ok := idx.sections[c.Section]
	if !ok {
		if idx.delegated[c.Section] {
			return sec, setupRequestError(fmt.Sprintf("section %q is a delegated section; the form cannot change it", c.Section))
		}
		return sec, setupRequestError(fmt.Sprintf("section %q is not a form section", c.Section))
	}
	if !slices.ContainsFunc(sec.Fields, func(f setupmeta.Field) bool { return f.Name == c.Field }) {
		return sec, setupRequestError(fmt.Sprintf("field %q is not a field of section %q", c.Field, c.Section))
	}
	pair := c.Section + "\x00" + c.Field
	if seen[pair] {
		return sec, setupRequestError(fmt.Sprintf("section %q field %q is in changes more than once", c.Section, c.Field))
	}
	seen[pair] = true
	switch c.Op {
	case "set":
		if c.Value == nil {
			return sec, setupRequestError(fmt.Sprintf("section %q field %q has op set with no value; use op reset to remove a value", c.Section, c.Field))
		}
	case "reset":
		if c.Value != nil {
			return sec, setupRequestError(fmt.Sprintf("section %q field %q has op reset with a value; op reset takes no value", c.Section, c.Field))
		}
	default:
		return sec, setupRequestError(fmt.Sprintf("op %q is not set or reset", c.Op))
	}
	return sec, nil
}

// checkSetupTargets checks that each target names a local form section with a
// change, and that each value is project or user. It checks the ids in sorted
// order.
func checkSetupTargets(targets map[string]string, byID map[string]*setupSaveSection) error {
	ids := make([]string, 0, len(targets))
	for id := range targets {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		s := byID[id]
		if s == nil || !setupLocalKey(s.key) {
			return setupRequestError(fmt.Sprintf("targets names section %q, which is not a local form section with a change", id))
		}
		if v := targets[id]; v != "project" && v != "user" {
			return setupRequestError(fmt.Sprintf("target %q is not project or user", v))
		}
	}
	return nil
}

// resolveSetupTargets sets the target of each changed section and returns the
// sections in form order. needUser is true when a section has the target user.
func resolveSetupTargets(targets map[string]string, order []string, byID map[string]*setupSaveSection) (out []*setupSaveSection, needUser bool) {
	for _, id := range order {
		s := byID[id]
		if s == nil {
			continue
		}
		switch {
		case !setupLocalKey(s.key):
			s.target = "config"
		case targets[id] != "":
			s.target = targets[id]
		case s.sec.DefaultTarget == "user":
			s.target = "user"
		default:
			s.target = "project"
		}
		needUser = needUser || s.target == "user"
		out = append(out, s)
	}
	return out, needUser
}

// setupRequestError is the DomainError of a broken request rule. msg names
// the rule.
func setupRequestError(msg string) error {
	return &mcpserver.DomainError{
		Msg:        "dashboard setup save: " + msg + ". Nothing was written.",
		Suggestion: dashboardSetupRequestSuggestion,
	}
}

// setupLocalKey reports whether the config path key lives in local.toml.
func setupLocalKey(key string) bool {
	top, _, _ := strings.Cut(key, ".")
	return !config.ProjectSections[top]
}

// setupSavePath returns the absolute path of the target file of a save
// section. target is "config", "project" or "user".
func setupSavePath(root, userPath, target, key string) string {
	if target == "user" {
		return setupTargetPath(root, root, userPath, "user", key)
	}
	return setupTargetPath(root, root, userPath, "", key)
}

// setupSaveNotTableError is the DataError of a stored value that is not a
// table where the save needs a table.
func setupSaveNotTableError(key, file string) error {
	return &mcpserver.DataError{
		Msg:        fmt.Sprintf("dashboard setup save: %s in %s is not a table.", key, file),
		Suggestion: fmt.Sprintf("Change %s in %s to a table, or remove the key. Then save again. Nothing was written.", key, file),
	}
}

// setupApplyChange sets or removes the field name in table. A dotted name
// sets or removes the key at its nested path, and set creates each absent
// parent table. A reset of the last key of a nested table leaves the empty
// table. badParent is the dotted path, relative to table, of a parent whose
// value is not a table; table is then not changed at that path. removed is
// true when a reset removed a key.
func setupApplyChange(table map[string]any, name, op string, value any) (badParent string, removed bool) {
	parts := strings.Split(name, ".")
	cur := table
	for i, part := range parts[:len(parts)-1] {
		next, present := cur[part]
		if !present {
			if op == "reset" {
				return "", false
			}
			child := map[string]any{}
			cur[part] = child
			cur = child
			continue
		}
		child, isTable := next.(map[string]any)
		if !isTable {
			return strings.Join(parts[:i+1], "."), false
		}
		cur = child
	}
	leaf := parts[len(parts)-1]
	if op == "set" {
		cur[leaf] = value
		return "", false
	}
	_, present := cur[leaf]
	delete(cur, leaf)
	return "", present
}

// setupDeepCopy copies the tables and the lists of v. Other values are
// returned as they are.
func setupDeepCopy(v any) any {
	switch val := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(val))
		for k, item := range val {
			out[k] = setupDeepCopy(item)
		}
		return out
	case []any:
		out := make([]any, len(val))
		for i, item := range val {
			out[i] = setupDeepCopy(item)
		}
		return out
	case []map[string]any:
		out := make([]map[string]any, len(val))
		for i, item := range val {
			out[i] = setupDeepCopy(item).(map[string]any)
		}
		return out
	default:
		return v
	}
}

// setupSaveCall sends sections to setupWriteFunc with target ("" or "user").
func setupSaveCall(root string, sections map[string]map[string]any, target string) (SetupWriteSectionsOut, error) {
	data, err := json.Marshal(sections)
	if err != nil {
		return SetupWriteSectionsOut{}, &mcpserver.DataError{
			Msg:        fmt.Sprintf("dashboard setup save: encode the sections: %s", err.Error()),
			Suggestion: "Check the config files for a value that is not a text, a number, a boolean, a list or a table. Nothing was written.",
			Cause:      err,
		}
	}
	return setupWriteFunc(root, root, SetupWriteSectionsIn{SectionsJSON: string(data), Target: target})
}

// setupSaveCollect adds the written and the failed sections, and the
// warnings, of one write call to out. sent holds the keys of that call.
func setupSaveCollect(out *DashboardSetupSaveOut, root, userPath, target string,
	sent map[string]map[string]any, res SetupWriteSectionsOut, byKey map[string]*setupSaveSection) {
	for _, key := range res.Written {
		s := byKey[key]
		if s == nil {
			continue
		}
		out.Written = append(out.Written, DashboardSetupWrite{
			Section: s.sec.ID,
			File:    setupTargetPath(root, root, userPath, target, key),
		})
	}
	for _, key := range sortedSetupKeys(sent) {
		if slices.Contains(res.Written, key) {
			continue
		}
		msg := "the section was not written"
		prefix := "section " + key + ": "
		for _, e := range res.Errors {
			if rest, ok := strings.CutPrefix(e, prefix); ok {
				msg = rest
				break
			}
		}
		out.Failed = append(out.Failed, DashboardSetupFailure{
			Section: byKey[key].sec.ID,
			File:    setupTargetPath(root, root, userPath, target, key),
			Message: msg,
		})
	}
	out.Warnings = append(out.Warnings, res.Warnings...)
}

// sortedSetupKeys returns the keys of m in sorted order.
func sortedSetupKeys(m map[string]map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// DashboardSetupCreate writes the first config of root through the init code
// of the setup tool. root is the main worktree of the repo. It refuses a repo
// whose state is not no-config.
func DashboardSetupCreate(root string) (DashboardSetupCreateOut, error) {
	state, err := setupRepoState(root)
	if err != nil {
		return DashboardSetupCreateOut{}, err
	}
	if state != SetupStateNoConfig {
		return DashboardSetupCreateOut{}, &DashboardSetupNotReady{State: state, Suggestion: dashboardSetupHasConfigSuggestion}
	}
	res, err := setupInitRoots(root, root, SetupInitIn{})
	if err != nil {
		return DashboardSetupCreateOut{}, err
	}
	out := DashboardSetupCreateOut{OK: res.OK, Created: res.Created, Changed: res.Changed, Errors: res.Errors}
	if out.Created == nil {
		out.Created = []string{}
	}
	if out.Changed == nil {
		out.Changed = []string{}
	}
	if out.Errors == nil {
		out.Errors = []string{}
	}
	return out, nil
}
