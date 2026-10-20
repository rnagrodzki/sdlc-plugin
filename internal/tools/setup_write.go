// Package tools: setup_write_sections tool (Task 44 addition).
//
// Gap this closes: setup_init (internal/tools/setup.go) writes complete
// config.toml/local.toml templates verbatim — it takes no field values, and
// config.WriteSection is otherwise unexposed to any tool. Some setup flows
// (and skills built on top of setup) still need to persist real,
// individually-collected field values (e.g. version: {mode: "file",
// versionFile: "package.json"}) into a section after the template has been
// dropped, rather than requiring the user to hand-edit every field.
//
// This is a small additive tool for that "writing config files" sub-flow,
// which needs to persist the field values collected via AskUserQuestion
// into the correct section. It does not modify setup_init or
// config.WriteSection — it is a second, narrower caller of the same
// read-merge-write primitive, taking real values instead of a template.
package tools

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/rnagrodzki/sdlc-plugin/internal/config"
	"github.com/rnagrodzki/sdlc-plugin/internal/fsx"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
	"github.com/rnagrodzki/sdlc-plugin/internal/setupmeta"
	"github.com/rnagrodzki/sdlc-plugin/internal/shipmeta"
	"github.com/rnagrodzki/sdlc-plugin/internal/worktree"
)

// SetupWriteSectionsIn is the input for the setup_write_sections tool.
type SetupWriteSectionsIn struct {
	// SectionsJSON is a JSON-encoded object mapping section id (e.g.
	// "version", "commit") to the full field-value object for that section,
	// e.g. {"version":{"mode":"file","versionFile":"package.json"}}. A
	// plain top-level id (e.g. "version") REPLACES that section wholesale.
	// A dotted id (e.g. "plan.guardrails") is routed by config.WriteSection
	// to the nested table at that LEAF path — siblings under the same
	// top-level key are preserved, and only the leaf is replaced wholesale.
	// Either way, callers must pass the complete object for the id they
	// name, not a partial patch of it. A JSON null on a guardrail id
	// (plan.guardrails.<id> or execute.guardrails.<id>) deletes that table
	// instead (see isGuardrailID). A key that replaces a whole guardrails table
	// (see guardedGuardrailKeys) must keep every guardrail id that config.toml
	// holds, or the call fails before any write (see droppedGuardrails).
	SectionsJSON string `json:"sectionsJson" jsonschema_description:"JSON-encoded object mapping section id (e.g. \"version\", \"commit\") to the full field-value object for that section, e.g. {\"version\":{\"mode\":\"file\",\"versionFile\":\"package.json\"}}. A plain top-level id REPLACES that section wholesale. A dotted id (e.g. \"plan.guardrails\") merges at that nested leaf instead, preserving sibling keys under the same top-level section — the leaf itself is still replaced wholesale, not patched. Pass the complete object for the id you name. A JSON null on a guardrail id (plan.guardrails.<id> or execute.guardrails.<id>) deletes that table. A null on a missing guardrail id changes nothing. A null on any other id clears it to an empty table, as before, unless that id is a whole guardrails table that holds guardrail ids (see the next sentence). A key that replaces a whole guardrails table (plan, plan.guardrails, execute, execute.guardrails), with a value or a null, must keep every guardrail id that config.toml holds: a key that drops one fails with a DomainError and nothing is written. Add or change one guardrail with \"<section>.guardrails.<id>\", and remove one with a null on that key. A guardrail table that holds a comment line is not deleted: the call returns the comment refusal."`

	// Target selects which file a local section is written to: the
	// project's own .sdlc-v2/local.toml (default) or the user-level file
	// shared across every project (see config.UserConfigPath). It has no
	// effect on project sections, which always go to config.toml — and is
	// rejected outright when a project section is named alongside it.
	Target string `json:"target,omitempty" jsonschema:"enum=project,enum=user" jsonschema_description:"Plain text. Where local sections go: project (.sdlc-v2/local.toml, default) or user (~/.sdlc/local.toml or $SDLC_USER_CONFIG, shared by all projects). Project sections always go to config.toml, and user is rejected for them. Example: user"`
}

// SetupWriteSectionsOut is the output for the setup_write_sections tool.
type SetupWriteSectionsOut struct {
	OK       bool                 `json:"ok"`
	Written  []string             `json:"written" jsonschema_description:"Section ids written with data. A deleted guardrail id is not listed here: see deleted."`
	Deleted  []DeletedSection     `json:"deleted,omitempty" jsonschema_description:"Guardrail ids whose table a JSON null deleted from config.toml, each with the data of the removed table."`
	Root     string               `json:"root" jsonschema_description:"Absolute path of the active git worktree that config.toml and scaffolded CI files were written under."`
	Errors   []string             `json:"errors,omitempty"`
	Scaffold []ScaffoldFileReport `json:"scaffold,omitempty"`
	Warnings []string             `json:"warnings,omitempty"`
	Next     string               `json:"next,omitempty" jsonschema_description:"Plain text. Recovery step when a section could not be written without deleting comments, or a note when a null on a guardrail id found no table to delete. Example: Edit section ship in .sdlc-v2/local.toml by hand, then run setup again for that section."`
}

// DeletedSection is one guardrail table that a JSON null deleted.
type DeletedSection struct {
	ID    string         `json:"id" jsonschema_description:"Plain text. The deleted guardrail id. Example: plan.guardrails.dry-old"`
	Table map[string]any `json:"table" jsonschema_description:"The data of the removed table, as it was in config.toml before the delete."`
}

// rewroteWarning is the warning for a section whose file could not be
// edited in place and was rewritten whole from parsed data. file names the
// file, as sectionFile returns it.
func rewroteWarning(id, file string) string {
	return fmt.Sprintf("Section %s: could not edit %s in place; the file had no comments, so it was rewritten and template tips were added", id, file)
}

// RegisterSetupWriteTools registers setup_write_sections on the server.
func RegisterSetupWriteTools(s *mcpserver.Server) {
	mcpserver.Register(s, "setup_write_sections",
		"INTERNAL — called by sdlc skills only. Writes real field-value data into one or more sdlc-v2 config sections (config.toml for project sections, local.toml for local sections), routing and validating via the same config.WriteSection primitive setup_init uses. Unlike setup_init (which writes the full config.toml/local.toml templates verbatim for the user to hand-edit), this accepts the actual assembled values collected during setup's per-section field loop. Git-tracked files (config.toml and the CI files scaffolded after a version write) go under the active git worktree, returned as root; local.toml goes under the main worktree, shared by all worktrees. With target \"user\", local sections go to the user-level file (~/.sdlc/local.toml or $SDLC_USER_CONFIG), outside every repository; a project section with target \"user\" is rejected and nothing is written. Each write changes only the lines of changed keys and keeps every comment line. A JSON null on a guardrail id (plan.guardrails.<id> or execute.guardrails.<id>) deletes that table from config.toml instead: the lines of the table and one blank line next to it go, and deleted lists the id with the removed data. A null on a missing guardrail id changes nothing and adds a warning. A guardrail table that holds a comment line is not deleted: errors gets the comment refusal. A null guardrail id with target \"user\" is rejected and nothing is written. Missing template tips are added back above their keys. A flag-set field (ship.steps, ship.quick) sent as an array of step names is stored as a table of every step set to true or false, and an old list line for that key is replaced. An unknown step name or a non-boolean value returns an error and nothing is written. If a section cannot be edited in place and the file has comments, that section is not written: errors gets \"section <id>: <reason>\" and next gets the hand-edit step. Other sections are still written. A key that replaces a whole guardrails table (plan, plan.guardrails, execute, execute.guardrails), with a value or a null, makes the tool read config.toml first. If that key drops a guardrail id that config.toml holds, the call fails with a DomainError that names the key and the ids, and nothing is written. If config.toml cannot be read, is not valid TOML, or is over 1 MiB, the call also fails before any write. A one-id key such as plan.guardrails.<id> needs no read.",
		mcpserver.Annotations{
			Title:       "Write SDLC config sections",
			ReadOnly:    false,
			Destructive: true,
			Idempotent:  false,
			OpenWorld:   false,
		},
		func(ctx mcpserver.Ctx, in SetupWriteSectionsIn) (SetupWriteSectionsOut, error) {
			contentRoot, err := worktree.ActiveRoot()
			if err != nil {
				contentRoot, err = os.Getwd()
				if err != nil {
					return SetupWriteSectionsOut{}, &mcpserver.InfraError{
						Msg:        fmt.Sprintf("resolve project root: %s", err.Error()),
						Suggestion: "Restart the sdlc MCP server from a directory that still exists, then retry setup_write_sections.",
						Cause:      err,
					}
				}
			}
			stateRoot, err := worktree.MainRoot()
			if err != nil {
				stateRoot, err = os.Getwd()
				if err != nil {
					return SetupWriteSectionsOut{}, &mcpserver.InfraError{
						Msg:        fmt.Sprintf("resolve project root: %s", err.Error()),
						Suggestion: "Restart the sdlc MCP server from a directory that still exists, then retry setup_write_sections.",
						Cause:      err,
					}
				}
			}
			return setupWriteSections(contentRoot, stateRoot, in)
		},
	)
}

// expandDottedKeys converts a flat field-value map that may use dotted key
// names (e.g. "tag.prefix") into the nested shape config.WriteSection
// expects (e.g. {"tag": {"prefix": ...}}). setupmeta.Field descriptors use
// dotted names so the setup skill can collect answers as a flat list; this
// expands them back into the nested VersionSection shape (and any other
// section that adopts dotted field names) before the write. Keys without a
// "." pass through unchanged. When two dotted keys share a prefix (e.g.
// "tag.enabled" and "tag.prefix"), their expansions merge into the same
// nested object.
func expandDottedKeys(flat map[string]any) map[string]any {
	out := make(map[string]any, len(flat))
	for key, val := range flat {
		parts := strings.Split(key, ".")
		if len(parts) == 1 {
			out[key] = val
			continue
		}
		cur := out
		for _, part := range parts[:len(parts)-1] {
			next, ok := cur[part].(map[string]any)
			if !ok {
				next = make(map[string]any)
				cur[part] = next
			}
			cur = next
		}
		cur[parts[len(parts)-1]] = val
	}
	return out
}

// normalizeFlagSetFields converts each flag-set field answer in a section
// from an array of selected names to a table of every option → bool.
// A table answer passes through unchanged (no keys are added), after a
// check that every value is a bool and every key is a known step.
// An unknown name, a non-bool table value or a scalar answer returns a
// DomainError. A JSON null answer stays in place: the write loop clears
// the key. The section is found by matching sectionID to the ConfigPath
// of a setupmeta section; an id with no match is left as it is. values is
// changed in place.
func normalizeFlagSetFields(sectionID string, values map[string]any) error {
	if values == nil {
		return nil
	}
	for _, sec := range setupmeta.Sections() {
		if sec.ConfigPath != sectionID {
			continue
		}
		for _, field := range sec.Fields {
			if field.Type != "flag-set" {
				continue
			}
			answer, present := values[field.Name]
			if !present || answer == nil {
				continue
			}
			path := sectionID + "." + field.Name
			switch v := answer.(type) {
			case []any:
				selected := make(map[string]bool, len(v))
				for _, item := range v {
					name, isString := item.(string)
					if !isString || !slices.Contains(field.Options, name) {
						return unknownFlagSetNameError(path, flagSetValueText(item))
					}
					selected[name] = true
				}
				table := make(map[string]any, len(field.Options))
				for _, option := range field.Options {
					table[option] = selected[option]
				}
				values[field.Name] = table
			case map[string]any:
				keys := make([]string, 0, len(v))
				for key := range v {
					keys = append(keys, key)
				}
				sort.Strings(keys)
				for _, key := range keys {
					if !slices.Contains(field.Options, key) {
						return unknownFlagSetNameError(path, fmt.Sprintf("%q", key))
					}
					if _, isBool := v[key].(bool); !isBool {
						return &mcpserver.DomainError{
							Msg:        fmt.Sprintf("%s.%s must be true or false, got %s.", path, key, flagSetValueText(v[key])),
							Suggestion: "Send true or false for each step, or send an array of step names.",
						}
					}
				}
			default:
				return &mcpserver.DomainError{
					Msg:        fmt.Sprintf("%s answer must be an array of step names or a table of step → true/false, got %s.", path, flagSetTypeName(answer)),
					Suggestion: "Send an array of step names, for example [\"execute\",\"review\"].",
				}
			}
		}
	}
	return nil
}

// unknownFlagSetNameError returns the DomainError for a flag-set answer that
// names a step outside the canonical step list. quotedName is the offending
// name, already quoted for the message.
func unknownFlagSetNameError(path, quotedName string) error {
	return &mcpserver.DomainError{
		Msg:        fmt.Sprintf("%s answer has an unknown step %s.", path, quotedName),
		Suggestion: fmt.Sprintf("Use only these names: %s.", strings.Join(shipmeta.CanonicalSteps, ", ")),
	}
}

// flagSetValueText renders one JSON value of a flag-set answer for an error
// message: a string is quoted, any other value is printed as is.
func flagSetValueText(v any) string {
	if s, ok := v.(string); ok {
		return fmt.Sprintf("%q", s)
	}
	return fmt.Sprintf("%v", v)
}

// flagSetTypeName names the type of a decoded flag-set answer for an error
// message. A string, bool or number gets its JSON type name. JSON decoding
// gives no other scalar type, so any other value gets its Go type name.
func flagSetTypeName(v any) string {
	switch v.(type) {
	case string:
		return "string"
	case bool:
		return "bool"
	case float64:
		return "number"
	default:
		return fmt.Sprintf("%T", v)
	}
}

// isGuardrailID reports whether id is plan.guardrails.<id> or
// execute.guardrails.<id>: the ids where a JSON null deletes the table.
func isGuardrailID(id string) bool {
	parts := strings.Split(id, ".")
	return len(parts) == 3 && (parts[0] == "plan" || parts[0] == "execute") &&
		parts[1] == "guardrails" && parts[2] != ""
}

// guardedGuardrailKeys maps each key that replaces a whole guardrails table
// to its section. plan and execute are included because a write at the
// section key replaces the guardrails table under it.
var guardedGuardrailKeys = map[string]string{
	"plan":               "plan",
	"plan.guardrails":    "plan",
	"execute":            "execute",
	"execute.guardrails": "execute",
}

// maxGuardrailCheckBytes bounds the config.toml read of the guardrail check.
// A real config.toml holds a few KiB.
const maxGuardrailCheckBytes = 1 << 20

// droppedGuardrails returns, for each guarded key in ids, the sorted guardrail
// ids that config.toml (under contentRoot) holds in that key's section and
// that the key's new value does not hold. It reads config.toml only when a
// guarded key is present. An absent file, or a value that is not a table of
// guardrails, holds no ids.
func droppedGuardrails(contentRoot string, ids []string, sections map[string]map[string]any) (map[string][]string, error) {
	var guarded []string
	for _, id := range ids {
		if _, ok := guardedGuardrailKeys[id]; ok {
			guarded = append(guarded, id)
		}
	}
	if len(guarded) == 0 {
		return nil, nil
	}
	path := filepath.Join(contentRoot, paths.DataDir, paths.ConfigFile)
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, guardReadError(path, err)
	}
	if info.Size() > maxGuardrailCheckBytes {
		return nil, &mcpserver.DataError{
			Msg:        fmt.Sprintf("setup_write_sections: %s is %d bytes, over the %d byte limit of the guardrail check", path, info.Size(), maxGuardrailCheckBytes),
			Suggestion: fmt.Sprintf("Write one guardrail at a time with \"<section>.guardrails.<id>\", which needs no check, or reduce %s below %d bytes, then retry. Nothing was written.", path, maxGuardrailCheckBytes),
		}
	}
	var raw map[string]any
	if err := fsx.ReadTOML(path, &raw); err != nil {
		if errors.Is(err, fsx.ErrParse) {
			return nil, &mcpserver.DataError{
				Msg:        fmt.Sprintf("setup_write_sections: %s is not valid TOML: %s", path, err.Error()),
				Suggestion: "Fix the TOML syntax in " + path + ", then retry. Nothing was written.",
				Cause:      err,
			}
		}
		return nil, guardReadError(path, err)
	}
	dropped := map[string][]string{}
	for _, id := range guarded {
		kept := newGuardrailIDs(id, sections[id])
		var gone []string
		for _, disk := range diskGuardrailIDs(raw, guardedGuardrailKeys[id]) {
			if !slices.Contains(kept, disk) {
				gone = append(gone, disk)
			}
		}
		if len(gone) > 0 {
			dropped[id] = gone
		}
	}
	return dropped, nil
}

// diskGuardrailIDs returns the sorted, unique guardrail ids under
// <section>.guardrails in raw: the keys of the named-table form whose value
// is a table, or the "id" fields of the array-of-tables form. The result
// equals the ids that normalizeGuardrailTables gives
// (internal/config/config.go:799-809). Any other value holds no ids.
func diskGuardrailIDs(raw map[string]any, section string) []string {
	sec, _ := raw[section].(map[string]any)
	seen := map[string]bool{}
	switch g := sec["guardrails"].(type) {
	case map[string]any:
		for id, v := range g {
			if _, ok := v.(map[string]any); !ok {
				continue
			}
			seen[id] = true
		}
	case []any:
		for _, entry := range g {
			if m, ok := entry.(map[string]any); ok {
				if id, ok := m["id"].(string); ok && id != "" {
					seen[id] = true
				}
			}
		}
	}
	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// newGuardrailIDs returns the guardrail ids that the new value of a guarded
// key holds. A nil value (JSON null) holds none. For "plan" and "execute"
// the ids are the keys of the value's "guardrails" table.
func newGuardrailIDs(key string, value map[string]any) []string {
	table := expandDottedKeys(value)
	if !strings.HasSuffix(key, ".guardrails") {
		table, _ = table["guardrails"].(map[string]any)
	}
	ids := make([]string, 0, len(table))
	for id := range table {
		ids = append(ids, id)
	}
	return ids
}

// guardReadError wraps a failure to stat or read config.toml in the
// InfraError of the guardrail check.
func guardReadError(path string, err error) error {
	return &mcpserver.InfraError{
		Msg:        fmt.Sprintf("setup_write_sections: read %s: %s", path, err.Error()),
		Suggestion: fmt.Sprintf("Check that %s is a readable file, then retry. Nothing was written.", path),
		Cause:      err,
	}
}

// guardrailDropError builds the refusal for the keys in dropped, in the
// sorted order of ids.
func guardrailDropError(ids []string, dropped map[string][]string) error {
	var parts []string
	for _, id := range ids {
		if gone, ok := dropped[id]; ok {
			parts = append(parts, fmt.Sprintf("key %q would remove guardrails %v from %s.guardrails", id, gone, guardedGuardrailKeys[id]))
		}
	}
	return &mcpserver.DomainError{
		Msg:        "setup_write_sections: " + strings.Join(parts, " and ") + ". Nothing was written.",
		Suggestion: "Write each guardrail as its own key, for example \"<section>.guardrails.<id>\". To remove a guardrail on purpose, send null on \"<section>.guardrails.<id>\". If config.toml holds the guardrails as an array of tables ([[<section>.guardrails]]), a one-id write can fail with an error or drop the other entries. In that case the user must first convert the array to named tables ([<section>.guardrails.<id>]) by hand. Nothing was written.",
	}
}

// sectionFile names, for use in messages, the config file a section id is
// written to. target and userPath mirror the tool input and the resolved
// config.UserConfigPath (empty when target != "user"): a project section
// always reports the project-relative config.toml path regardless of
// target (project sections never route to the user file), while a local
// section reports userPath verbatim when target is "user" — userPath can be
// anywhere on disk (SDLC_USER_CONFIG, or ~/.sdlc/local.toml), so unlike the
// project-relative literals below it is not safe to shorten to a fixed
// string.
func sectionFile(id, target, userPath string) string {
	top, _, _ := strings.Cut(id, ".")
	if config.ProjectSections[top] {
		return ".sdlc-v2/config.toml"
	}
	if target == "user" {
		return userPath
	}
	return ".sdlc-v2/local.toml"
}

// sectionRoot returns the worktree root a section id's file is written
// under: contentRoot for project sections (config.toml, git-tracked, so it
// belongs in the active worktree being worked on), stateRoot for local
// sections (local.toml, gitignored and shared by all worktrees off the same
// main repo).
func sectionRoot(contentRoot, stateRoot, id string) string {
	top, _, _ := strings.Cut(id, ".")
	if config.ProjectSections[top] {
		return contentRoot
	}
	return stateRoot
}

// setupWriteSections is the core logic, separated from the handler for
// testability. contentRoot holds git-tracked files (config.toml, scaffolded
// CI files); stateRoot holds gitignored local.toml shared by all worktrees.
func setupWriteSections(contentRoot, stateRoot string, in SetupWriteSectionsIn) (SetupWriteSectionsOut, error) {
	if in.SectionsJSON == "" {
		return SetupWriteSectionsOut{}, &mcpserver.DomainError{
			Msg:        "setup_write_sections: sectionsJson is required",
			Suggestion: "Pass a JSON-encoded object mapping section id to its full field-value object in sectionsJson, e.g. {\"version\":{\"mode\":\"file\"}}.",
		}
	}

	var sections map[string]map[string]any
	if err := json.Unmarshal([]byte(in.SectionsJSON), &sections); err != nil {
		return SetupWriteSectionsOut{}, &mcpserver.DomainError{
			Msg:        fmt.Sprintf("setup_write_sections: invalid sectionsJson: %s", err.Error()),
			Suggestion: "Fix the JSON syntax in sectionsJson so it parses as an object of section id to field-value object, then retry.",
			Cause:      err,
		}
	}
	if len(sections) == 0 {
		return SetupWriteSectionsOut{}, &mcpserver.DomainError{
			Msg:        "setup_write_sections: sectionsJson must contain at least one section",
			Suggestion: "Pick a section id from setup_prepare's sections list, then add its field-value object to sectionsJson before calling again.",
		}
	}

	// Sort ids for deterministic output ordering.
	ids := make([]string, 0, len(sections))
	for id := range sections {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	// Reject unknown top-level keys before writing anything. Without this,
	// config.WriteSection routes every non-project key to local.toml, so a
	// typo such as "shp" is written silently. An empty first segment is left
	// to config.WriteSection, which reports it as an invalid section name.
	var unknown []string
	for _, id := range ids {
		top, _, _ := strings.Cut(id, ".")
		if top != "" && !config.ProjectSections[top] && !config.LocalSections[top] {
			unknown = append(unknown, id)
		}
	}
	if len(unknown) > 0 {
		allowed := make([]string, 0, len(config.ProjectSections)+len(config.LocalSections))
		for k := range config.ProjectSections {
			allowed = append(allowed, k)
		}
		for k := range config.LocalSections {
			allowed = append(allowed, k)
		}
		sort.Strings(allowed)
		return SetupWriteSectionsOut{}, &mcpserver.DomainError{
			Msg:        fmt.Sprintf("setup_write_sections: unknown section keys %v; allowed top-level keys: %v", unknown, allowed),
			Suggestion: "Fix each unknown key so its first segment is one of the allowed top-level keys (use the section's configPath from setup_prepare), then retry. Nothing was written.",
		}
	}

	if in.Target != "" && in.Target != "project" && in.Target != "user" {
		return SetupWriteSectionsOut{}, &mcpserver.DomainError{
			Msg:        fmt.Sprintf("setup_write_sections: invalid target %q", in.Target),
			Suggestion: "Set target to project or user, or leave it empty.",
		}
	}

	// A user-level write only ever applies to local sections: config.toml
	// (project sections) is git-tracked and per-project by design, so
	// routing it to the user file would silently defeat that. Reject the
	// whole call — rather than writing the local sections and dropping the
	// project ones — so a caller never gets a partial, surprising write.
	var userPath string
	if in.Target == "user" {
		var guardrailIDs []string
		for _, id := range ids {
			if sections[id] == nil && isGuardrailID(id) {
				guardrailIDs = append(guardrailIDs, id)
			}
		}
		if len(guardrailIDs) > 0 {
			return SetupWriteSectionsOut{}, &mcpserver.DomainError{
				Msg:        fmt.Sprintf("setup_write_sections: guardrail ids live in config.toml: %v cannot be deleted with target \"user\"", guardrailIDs),
				Suggestion: "send the null with target project. Nothing was written.",
			}
		}

		var projectIDs []string
		for _, id := range ids {
			top, _, _ := strings.Cut(id, ".")
			if config.ProjectSections[top] {
				projectIDs = append(projectIDs, id)
			}
		}
		if len(projectIDs) > 0 {
			return SetupWriteSectionsOut{}, &mcpserver.DomainError{
				Msg:        fmt.Sprintf("setup_write_sections: project sections %v cannot use target \"user\"", projectIDs),
				Suggestion: "Remove the project section(s) from sectionsJson, or drop target (or set it to \"project\") and retry. Nothing was written.",
			}
		}

		var ok bool
		userPath, ok = config.UserConfigPath()
		if !ok {
			return SetupWriteSectionsOut{}, &mcpserver.DomainError{
				Msg:        fmt.Sprintf("setup_write_sections: no user config path available (%s unset, no home directory found)", config.UserConfigPathEnv),
				Suggestion: fmt.Sprintf("Set %s to a file path, or use target project.", config.UserConfigPathEnv),
			}
		}
	}

	// Convert flag-set answers before any write. A bad answer writes nothing.
	for _, id := range ids {
		if err := normalizeFlagSetFields(id, sections[id]); err != nil {
			return SetupWriteSectionsOut{}, err
		}
	}

	// Refuse a whole-table guardrail write that drops an id before any write.
	dropped, err := droppedGuardrails(contentRoot, ids, sections)
	if err != nil {
		return SetupWriteSectionsOut{}, err
	}
	if len(dropped) > 0 {
		return SetupWriteSectionsOut{}, guardrailDropError(ids, dropped)
	}

	var written []string
	var deleted []DeletedSection
	var errs []string
	var warnings []string
	var refused []string // one "Edit section <id> in <file> by hand" per ErrWouldDropComments section
	var absent []string  // guardrail ids whose null found no table to delete
	for _, id := range ids {
		value := sections[id]
		if value == nil && isGuardrailID(id) {
			rep, err := config.DeleteSectionReport(sectionRoot(contentRoot, stateRoot, id), id)
			if err != nil {
				errs = append(errs, fmt.Sprintf("section %s: %s", id, err.Error()))
				if errors.Is(err, config.ErrWouldDropComments) {
					refused = append(refused, fmt.Sprintf("Edit section %s in %s by hand", id, sectionFile(id, in.Target, userPath)))
				}
				continue
			}
			if !rep.Deleted {
				absent = append(absent, id)
				warnings = append(warnings, fmt.Sprintf("%s: not present, nothing deleted", id))
				continue
			}
			if rep.Rewrote {
				warnings = append(warnings, rewroteWarning(id, sectionFile(id, in.Target, userPath)))
			}
			deleted = append(deleted, DeletedSection{ID: id, Table: rep.Table})
			continue
		}
		if value == nil {
			value = map[string]any{}
		}
		value = config.WholeNumbersToInt(expandDottedKeys(value)).(map[string]any)

		var rewrote bool
		var err error
		if in.Target == "user" {
			// Already confirmed above that every id here is a local
			// section (project sections were rejected before this loop),
			// so this always writes userPath, never config.toml.
			rewrote, err = config.WriteFileSection(userPath, id, value)
		} else {
			rewrote, err = config.WriteSectionReport(sectionRoot(contentRoot, stateRoot, id), id, value)
		}
		if err != nil {
			errs = append(errs, fmt.Sprintf("section %s: %s", id, err.Error()))
			if errors.Is(err, config.ErrWouldDropComments) {
				refused = append(refused, fmt.Sprintf("Edit section %s in %s by hand", id, sectionFile(id, in.Target, userPath)))
			}
			continue
		}
		if rewrote {
			warnings = append(warnings, rewroteWarning(id, sectionFile(id, in.Target, userPath)))
		}
		written = append(written, id)
	}

	var next string
	switch len(refused) {
	case 0:
	case 1:
		next = refused[0] + ", then run setup again for that section. Other sections were written."
	default:
		next = strings.Join(refused, "; ") + ", then run setup again for those sections. Other sections were written."
	}
	if len(absent) > 0 {
		note := fmt.Sprintf("Nothing was deleted for %s: no such guardrail table. Check the id if you expected a change.", strings.Join(absent, ", "))
		if next != "" {
			note = next + " " + note
		}
		next = note
	}

	out := SetupWriteSectionsOut{OK: len(errs) == 0, Written: written, Deleted: deleted, Root: contentRoot, Warnings: warnings, Next: next}
	if len(errs) > 0 {
		out.Errors = errs
	}

	// Auto-trigger scaffold_ci after the version section is written. This is
	// best-effort: config write is the critical path, so scaffold errors are
	// surfaced as warnings, never as failures.
	for _, id := range written {
		if id == "version" {
			scaffoldOut, err := scaffoldCI(contentRoot, false)
			if err != nil {
				out.Warnings = append(out.Warnings, fmt.Sprintf("scaffold_ci: %s", err.Error()))
			} else {
				out.Scaffold = scaffoldOut.Files
				out.Warnings = append(out.Warnings, scaffoldOut.Warnings...)
			}
			break
		}
	}

	return out, nil
}
