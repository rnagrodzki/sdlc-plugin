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
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/rnagrodzki/sdlc-plugin/internal/config"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/setupmeta"
)

// maxSetupChanges is the largest number of entries in one request. The
// count rule runs before each other request rule. A valid save holds at most
// one change for each form field of setupmeta.Sections();
// TestDashboardSetupSave_MaxChangesCoversFields checks that this limit is not
// below that count.
const maxSetupChanges = 64

// The ops of a change.
const (
	setupOpSet   = "set"
	setupOpReset = "reset"
)

// The targets of a save section, and the sources of a field value.
const (
	setupTargetConfig  = "config"  // .sdlc-v2/config.toml
	setupTargetProject = "project" // .sdlc-v2/local.toml
	setupTargetUser    = "user"    // the user file (config.UserConfigPath)
)

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
	Section    string `json:"section"` // form section id
	File       string `json:"file"`
	Message    string `json:"message"`
	Suggestion string `json:"suggestion,omitempty"` // the recovery step of the error; absent when there is none
}

// DashboardSetupSaveOut is the result of a save.
type DashboardSetupSaveOut struct {
	OK       bool                    `json:"ok"`
	Written  []DashboardSetupWrite   `json:"written"`  // [] when none
	Failed   []DashboardSetupFailure `json:"failed"`   // [] when none
	Warnings []string                `json:"warnings"` // [] when none
	CIFiles  []string                `json:"ciFiles"`  // CI script files that the save created; [] when none
	Next     string                  `json:"next"`     // hand-edit step for a section that could not be written without deleting comments; "" when none
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
	return "dashboard setup save: " + setupFieldErrorsText(e.Fields)
}

// DashboardSetupNotReady is the error of a save or a create in a repo
// whose state does not allow it.
type DashboardSetupNotReady struct{ State, Suggestion string }

// Error names the state. The Suggestion text stays in its own field.
func (e *DashboardSetupNotReady) Error() string {
	return fmt.Sprintf("dashboard setup: the repo state %s does not allow this change", e.State)
}

// setupWriteFunc is the write call of the save. A test replaces it to make
// the second write fail (the same pattern as mkdirTempFunc, writeFileFunc and
// readFileFunc in fsseam.go).
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
	target  string                 // setupTargetConfig | setupTargetProject | setupTargetUser
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
	doc, err := readCheckedTOML(path, setupSaveReader)
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
// and applies the changes to a copy of it. For a user target it also reads
// the table of the project file, and resets each changed key in a copy of it.
func readSetupSaveSection(root, userPath string, s *setupSaveSection, docs setupDocCache) error {
	var err error
	file := setupSavePath(root, userPath, s.target, s.key)
	if s.stored, s.next, _, err = readSetupSaveTable(docs, file, s.key, s.changes, false); err != nil {
		return err
	}
	if s.target != setupTargetUser {
		return nil
	}
	projectFile := setupSavePath(root, userPath, setupTargetProject, s.key)
	_, s.project, s.projectRemoved, err = readSetupSaveTable(docs, projectFile, s.key, s.changes, true)
	return err
}

// readSetupSaveTable reads the table at the config path key from file. It
// returns the table and a copy of it with each change applied. resetAll
// true resets each changed field in the copy in place of the op of the
// change. removed is true when a reset removed a key.
func readSetupSaveTable(docs setupDocCache, file, key string, changes []DashboardSetupChange, resetAll bool) (stored, next map[string]any, removed bool, err error) {
	doc, err := docs.read(file)
	if err != nil {
		return nil, nil, false, err
	}
	stored, ok := setupTable(doc, key)
	if !ok {
		return nil, nil, false, setupSaveNotTableError(key, file)
	}
	next = setupCopyTable(stored)
	for _, c := range changes {
		op, value := c.Op, c.Value
		if resetAll {
			op, value = setupOpReset, nil
		}
		bad, rm := setupApplyChange(next, c.Field, op, value)
		if bad != "" {
			return nil, nil, false, setupSaveNotTableError(key+"."+bad, file)
		}
		removed = removed || rm
	}
	return stored, next, removed, nil
}

// checkSetupSaveValues turns flag lists to tables and the text of a rate
// field to a number, then checks the values of each new section.
func checkSetupSaveValues(sections []*setupSaveSection) error {
	newSections := map[string]map[string]any{}
	storedByKey := map[string]map[string]any{}
	for _, s := range sections {
		if err := normalizeFlagSetFields(s.key, s.next); err != nil {
			return err
		}
		normalizeSetupRates(s.key, s.next)
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
		if s.target == setupTargetUser {
			userSections[s.key] = s.next
		}
	}
	userWritten := map[string]bool{}
	if len(userSections) == 0 {
		return userWritten, false, nil
	}
	res, err := setupSaveCall(root, userSections, setupTargetUser)
	if err != nil {
		return nil, false, err
	}
	for _, key := range res.Written {
		userWritten[key] = true
	}
	setupSaveCollect(out, root, userPath, setupTargetUser, userSections, res, byKey)
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
		case s.target != setupTargetUser:
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
			out.Failed = append(out.Failed, setupFailure(byKey[key].sec.ID, setupTargetPath(root, root, userPath, "", key), err))
		}
		return nil
	}
	setupSaveCollect(out, root, userPath, "", projectSections, res, byKey)
	for _, rep := range res.Scaffold {
		if scaffoldActionWrites(rep.Action) {
			out.CIFiles = append(out.CIFiles, rep.Path)
		}
	}
	return nil
}

// setupFailure is the failure of a section whose write call returned err. The
// message is the message of err, with the text of its cause when the message
// does not hold it. The suggestion is the Suggestion of err.
func setupFailure(section, file string, err error) DashboardSetupFailure {
	_, msg, suggestion := mcpserver.ErrorParts(err)
	if cause := errors.Unwrap(err); cause != nil && !strings.Contains(msg, cause.Error()) {
		msg += ": " + cause.Error()
	}
	return DashboardSetupFailure{Section: section, File: file, Message: msg, Suggestion: suggestion}
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
			return sec, setupRequestError(fmt.Sprintf("section %q is a delegated section; the form cannot change it. Form sections: %s", c.Section, strings.Join(idx.order, ", ")))
		}
		return sec, setupRequestError(fmt.Sprintf("section %q is not a form section. Form sections: %s", c.Section, strings.Join(idx.order, ", ")))
	}
	if !slices.ContainsFunc(sec.Fields, func(f setupmeta.Field) bool { return f.Name == c.Field }) {
		names := make([]string, 0, len(sec.Fields))
		for _, f := range sec.Fields {
			names = append(names, f.Name)
		}
		return sec, setupRequestError(fmt.Sprintf("field %q is not a field of section %q. Fields: %s", c.Field, c.Section, strings.Join(names, ", ")))
	}
	pair := c.Section + "\x00" + c.Field
	if seen[pair] {
		return sec, setupRequestError(fmt.Sprintf("section %q field %q is in changes more than once", c.Section, c.Field))
	}
	seen[pair] = true
	switch c.Op {
	case setupOpSet:
		if c.Value == nil {
			return sec, setupRequestError(fmt.Sprintf("section %q field %q has op set with no value; use op reset to remove a value", c.Section, c.Field))
		}
	case setupOpReset:
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
		if v := targets[id]; v != setupTargetProject && v != setupTargetUser {
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
			s.target = setupTargetConfig
		case targets[id] != "":
			s.target = targets[id]
		case s.sec.DefaultTarget == setupTargetUser:
			s.target = setupTargetUser
		default:
			s.target = setupTargetProject
		}
		needUser = needUser || s.target == setupTargetUser
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
// section. target is setupTargetConfig, setupTargetProject or setupTargetUser.
func setupSavePath(root, userPath, target, key string) string {
	if target == setupTargetUser {
		return setupTargetPath(root, root, userPath, setupTargetUser, key)
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
			if op == setupOpReset {
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
	if op == setupOpSet {
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
		return setupCopyTable(val)
	case []any:
		out := make([]any, len(val))
		for i, item := range val {
			out[i] = setupDeepCopy(item)
		}
		return out
	case []map[string]any:
		out := make([]map[string]any, len(val))
		for i, item := range val {
			out[i] = setupCopyTable(item)
		}
		return out
	default:
		return v
	}
}

// setupCopyTable returns a deep copy of the table m (setupDeepCopy).
func setupCopyTable(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, item := range m {
		out[k] = setupDeepCopy(item)
	}
	return out
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

// setupSaveCollect adds the written and the failed sections, the warnings,
// and the hand-edit step of one write call to out. sent holds the keys of
// that call. The message of a failed section is its entry in
// res.sectionErrs.
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
		msg, ok := res.sectionErrs[key]
		if !ok {
			msg = "setup_write_sections did not write the section and gave no reason"
		}
		out.Failed = append(out.Failed, DashboardSetupFailure{
			Section: byKey[key].sec.ID,
			File:    setupTargetPath(root, root, userPath, target, key),
			Message: msg,
		})
	}
	out.Warnings = append(out.Warnings, res.Warnings...)
	if res.Next != "" {
		if out.Next != "" {
			out.Next += " "
		}
		out.Next += res.Next
	}
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
	return DashboardSetupCreateOut{OK: res.OK, Created: nonNilStrings(res.Created), Changed: nonNilStrings(res.Changed), Errors: nonNilStrings(res.Errors)}, nil
}
