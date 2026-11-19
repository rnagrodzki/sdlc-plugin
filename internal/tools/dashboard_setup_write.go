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
	"maps"
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

// The ops of a guardrail change.
const (
	setupGuardrailOpAdd    = "add"
	setupGuardrailOpChange = "change"
	setupGuardrailOpRemove = "remove"
)

// maxGuardrailChangesPerList is the largest number of changes of one
// guardrails list in one save. It bounds the merged rule check and the error
// list.
const maxGuardrailChangesPerList = 100

// guardrailIDMaxBytes is the longest id of an add, change or remove. The id
// pattern of validateOneGuardrail has no maximum of its own.
const guardrailIDMaxBytes = 64

// DashboardSetupGuardrailChange is one change of a guardrails list.
// add and change need both Severity and Description. change replaces both
// stored fields. remove takes neither.
type DashboardSetupGuardrailChange struct {
	ID          string `json:"id"`
	Op          string `json:"op"`                    // "add" | "change" | "remove"
	Severity    string `json:"severity,omitempty"`    // add, change: "error" | "warning"
	Description string `json:"description,omitempty"` // add, change: 1 to guardrailDescMaxBytes (1024) bytes
}

// DashboardSetupSaveIn is the request of a save.
type DashboardSetupSaveIn struct {
	Changes []DashboardSetupChange `json:"changes"`
	Targets map[string]string      `json:"targets"` // form section id of a local section -> "project" | "user"
	// Guardrails maps a guardrail section id ("plan-guardrails" |
	// "execution-guardrails") to its changes. Omitted or empty: no guardrail
	// change. A listed section needs 1 to maxGuardrailChangesPerList changes,
	// each id at most once.
	Guardrails map[string][]DashboardSetupGuardrailChange `json:"guardrails,omitempty"`
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
	// One cache serves the form save and the guardrail save, so both give the
	// same outcome for each state of config.toml.
	docs := setupDocCache{}
	if err := readSetupSaveSections(root, userPath, sections, docs); err != nil {
		return DashboardSetupSaveOut{}, err
	}
	if err := checkSetupSaveValues(sections); err != nil {
		return DashboardSetupSaveOut{}, err
	}
	gr, err := checkSetupGuardrails(root, userPath, in, docs)
	if err != nil {
		return DashboardSetupSaveOut{}, err
	}
	_ = gr // the write of the guardrail keys joins writeSetupSave in a later change
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

// readSetupSaveSections reads each target file one time through docs. For
// each section it sets stored and next, and for a user target also project.
func readSetupSaveSections(root, userPath string, sections []*setupSaveSection, docs setupDocCache) error {
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
	// The guardrail rules hold the empty rule of the request: a save with no
	// form change is valid when it has a guardrail change.
	if err := checkSetupGuardrailRequest(in); err != nil {
		return nil, "", err
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

// setupGuardrailRequestError is the DomainError of a broken guardrail request
// rule. msg names the rule; suggestion is the recovery step of that rule.
func setupGuardrailRequestError(msg, suggestion string) error {
	return &mcpserver.DomainError{
		Msg:        "dashboard setup save: " + msg + ". Nothing was written.",
		Suggestion: suggestion,
	}
}

// setupGuardrailWrite is one guardrail key to write. The map key of
// setupGuardrailWrites is the write key "<config path>.<id>", for example
// "plan.guardrails.dry".
type setupGuardrailWrite struct {
	section string         // "plan-guardrails" | "execution-guardrails"
	id      string         // guardrail id
	value   map[string]any // {"description": d, "severity": s}; nil for a remove
}

// setupGuardrailWrites maps each write key to its guardrail write.
type setupGuardrailWrites map[string]setupGuardrailWrite

// setupGuardrailPaths maps each guardrail section id of setupmeta.Sections()
// to its config path: plan-guardrails to plan.guardrails, and
// execution-guardrails to execute.guardrails.
func setupGuardrailPaths() map[string]string {
	out := map[string]string{}
	for _, sec := range setupmeta.Sections() {
		if sec.ID == "plan-guardrails" || sec.ID == "execution-guardrails" {
			out[sec.ID] = sec.ConfigPath
		}
	}
	return out
}

// checkSetupGuardrailRequest checks the request rules of the guardrail
// changes, and the rule that a save has a form change or a guardrail change.
// It reads no file. It checks the section keys in sorted order, and the
// changes of each section in request order.
func checkSetupGuardrailRequest(in DashboardSetupSaveIn) error {
	if len(in.Changes) == 0 && len(in.Guardrails) == 0 {
		return setupGuardrailRequestError("changes and guardrails are empty", "Change a field or a guardrail, then save.")
	}
	paths := setupGuardrailPaths()
	for _, key := range slices.Sorted(maps.Keys(in.Guardrails)) {
		if _, ok := paths[key]; !ok {
			return setupGuardrailRequestError("guardrails has unknown section "+key, "Use plan-guardrails or execution-guardrails.")
		}
		list := in.Guardrails[key]
		if len(list) == 0 {
			return setupGuardrailRequestError("guardrails."+key+" is empty", "Omit a section with no change.")
		}
		if len(list) > maxGuardrailChangesPerList {
			return setupGuardrailRequestError(
				fmt.Sprintf("guardrails.%s has %d changes, over the limit of %d", key, len(list), maxGuardrailChangesPerList),
				fmt.Sprintf("Save at most %d guardrail changes of one list at one time.", maxGuardrailChangesPerList))
		}
		seen := map[string]bool{}
		for _, c := range list {
			if err := checkSetupGuardrailChange(key, c, seen); err != nil {
				return err
			}
		}
	}
	return nil
}

// checkSetupGuardrailChange checks the repeat, the id length, the op, and the
// fields of one guardrail change of the section key, in this order. It
// records the id in seen.
func checkSetupGuardrailChange(key string, c DashboardSetupGuardrailChange, seen map[string]bool) error {
	if seen[c.ID] {
		return setupGuardrailRequestError(fmt.Sprintf("guardrails.%s changes id %s twice", key, c.ID), "Send one change for each id.")
	}
	seen[c.ID] = true
	if n := len(c.ID); n > guardrailIDMaxBytes {
		return setupGuardrailRequestError(
			fmt.Sprintf("guardrails.%s has an id of %d bytes, over the limit of %d", key, n, guardrailIDMaxBytes),
			fmt.Sprintf("Use an id of at most %d bytes.", guardrailIDMaxBytes))
	}
	switch c.Op {
	case setupGuardrailOpAdd, setupGuardrailOpChange:
		if c.Severity == "" || c.Description == "" {
			return setupGuardrailRequestError(fmt.Sprintf("guardrails.%s: %s of %s needs severity and description", key, c.Op, c.ID),
				"Send both fields. change replaces both stored values.")
		}
	case setupGuardrailOpRemove:
		if c.Severity != "" || c.Description != "" {
			return setupGuardrailRequestError(fmt.Sprintf("guardrails.%s: remove of %s takes no severity or description", key, c.ID),
				"Send only id and op for a remove.")
		}
	default:
		return setupGuardrailRequestError(fmt.Sprintf("guardrails.%s: op %s is not add, change or remove", key, c.Op), "Use add, change or remove.")
	}
	return nil
}

// checkSetupGuardrails checks the guardrail changes of in against config.toml
// and returns the keys to write. It returns an empty map, and reads no file,
// when in has no guardrail change. Otherwise it applies the request rules,
// reads config.toml through docs, refuses a list in array form or an entry
// that is not a table, refuses each stale change, and checks the merged list
// with the per-entry guardrail rules. It does not check a lower severity: an
// operator may change error to warning. A stale change or a rule finding is a
// *DashboardSetupInvalid.
func checkSetupGuardrails(root, userPath string, in DashboardSetupSaveIn, docs setupDocCache) (setupGuardrailWrites, error) {
	writes := setupGuardrailWrites{}
	if len(in.Guardrails) == 0 {
		return writes, nil
	}
	if err := checkSetupGuardrailRequest(in); err != nil {
		return nil, err
	}
	file := setupSavePath(root, userPath, setupTargetConfig, "plan")
	doc, err := docs.read(file)
	if err != nil {
		return nil, err
	}
	paths := setupGuardrailPaths()
	keys := slices.Sorted(maps.Keys(in.Guardrails))
	disk := map[string]map[string]map[string]any{}
	var stale []SetupFieldError
	for _, key := range keys {
		list, err := setupGuardrailDiskList(doc, paths[key], file)
		if err != nil {
			return nil, err
		}
		disk[key] = list
		stale = append(stale, setupGuardrailStale(key, paths[key], in.Guardrails[key], list)...)
	}
	if len(stale) > 0 {
		return nil, setupGuardrailInvalid(stale)
	}
	var findings []SetupFieldError
	for _, key := range keys {
		path := paths[key]
		merged := mergeSetupGuardrails(disk[key], in.Guardrails[key])
		findings = append(findings, checkSetupGuardrailList(key, path, merged)...)
		for _, c := range in.Guardrails[key] {
			w := setupGuardrailWrite{section: key, id: c.ID}
			if c.Op != setupGuardrailOpRemove {
				w.value = map[string]any{"description": c.Description, "severity": c.Severity}
			}
			writes[path+"."+c.ID] = w
		}
	}
	if len(findings) > 0 {
		return nil, setupGuardrailInvalid(findings)
	}
	return writes, nil
}

// setupGuardrailDiskList returns the guardrails at the config path path of
// doc, by id. An absent parent table or an absent key gives an empty map. A
// list in array form ([[path]] or an inline array) is a DomainError. A path,
// a parent, or an entry that is not a table is a DataError. file is the path
// of config.toml, for the error text.
func setupGuardrailDiskList(doc map[string]any, path, file string) (map[string]map[string]any, error) {
	parentKey, leaf, _ := strings.Cut(path, ".")
	parent, ok := setupTable(doc, parentKey)
	if !ok {
		return nil, setupGuardrailNotTableError(parentKey, file)
	}
	raw, present := parent[leaf]
	if !present {
		return map[string]map[string]any{}, nil
	}
	switch list := raw.(type) {
	case []any, []map[string]any:
		return nil, setupGuardrailRequestError(
			fmt.Sprintf("config.toml holds %s as an array ([[%s]] or an inline array). The dashboard edits only [%s.<id>] tables", path, path, path),
			fmt.Sprintf("Change the list in config.toml to [%s.<id>] tables manually, then reload the page.", path))
	case map[string]any:
		out := make(map[string]map[string]any, len(list))
		for _, id := range slices.Sorted(maps.Keys(list)) {
			entry, isTable := list[id].(map[string]any)
			if !isTable {
				return nil, setupGuardrailNotTableError(path+"."+id, file)
			}
			out[id] = entry
		}
		return out, nil
	}
	return nil, setupGuardrailNotTableError(path, file)
}

// setupGuardrailNotTableError is the DataError of a guardrail key in
// config.toml that is not a table where the save needs a table.
func setupGuardrailNotTableError(key, file string) error {
	return &mcpserver.DataError{
		Msg:        fmt.Sprintf("dashboard setup save: %s in %s is not a table. Nothing was written.", key, file),
		Suggestion: fmt.Sprintf("Fix %s in config.toml manually.", key),
	}
}

// setupGuardrailStale returns one field error for each stale change of the
// section key: an add of an id that disk holds, or a change or a remove of
// an id that disk does not hold.
func setupGuardrailStale(key, path string, changes []DashboardSetupGuardrailChange, disk map[string]map[string]any) []SetupFieldError {
	var out []SetupFieldError
	for _, c := range changes {
		_, onDisk := disk[c.ID]
		var msg string
		switch {
		case c.Op == setupGuardrailOpAdd && onDisk:
			msg = fmt.Sprintf("guardrail %s is already in config.toml, so the add is stale. Reload the page.", c.ID)
		case c.Op != setupGuardrailOpAdd && !onDisk:
			msg = fmt.Sprintf("guardrail %s is not in config.toml, so the %s is stale. Reload the page.", c.ID, c.Op)
		default:
			continue
		}
		out = append(out, SetupFieldError{Section: key, Field: c.ID, Path: path + "." + c.ID, Message: msg})
	}
	return out
}

// mergeSetupGuardrails returns a copy of disk with each change applied: a
// remove deletes the id, and an add or a change sets the description and the
// severity of the change.
func mergeSetupGuardrails(disk map[string]map[string]any, changes []DashboardSetupGuardrailChange) map[string]map[string]any {
	out := make(map[string]map[string]any, len(disk))
	for id, g := range disk {
		out[id] = setupCopyTable(g)
	}
	for _, c := range changes {
		if c.Op == setupGuardrailOpRemove {
			delete(out, c.ID)
			continue
		}
		out[c.ID] = map[string]any{"description": c.Description, "severity": c.Severity}
	}
	return out
}

// checkSetupGuardrailList checks each entry of the merged list of the
// section key with validateOneGuardrail, in id order. A named table has no
// id key, so the check sets it from the table key. It returns one field
// error for each finding.
func checkSetupGuardrailList(key, path string, merged map[string]map[string]any) []SetupFieldError {
	var out []SetupFieldError
	seenIDs := map[string]bool{}
	for _, id := range slices.Sorted(maps.Keys(merged)) {
		g := merged[id]
		g["id"] = id
		for _, f := range validateOneGuardrail(g, seenIDs) {
			out = append(out, SetupFieldError{Section: key, Field: id, Path: path + "." + id, Message: f.Message + ". " + f.Fix})
		}
	}
	return out
}

// setupGuardrailInvalid is the *DashboardSetupInvalid of fields, sorted by
// path.
func setupGuardrailInvalid(fields []SetupFieldError) error {
	sort.SliceStable(fields, func(i, j int) bool { return fields[i].Path < fields[j].Path })
	return &DashboardSetupInvalid{Fields: fields}
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
