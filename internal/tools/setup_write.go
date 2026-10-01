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
	"fmt"
	"math"
	"os"
	"sort"
	"strings"

	"github.com/rnagrodzki/sdlc-plugin/internal/config"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
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
	// name, not a partial patch of it.
	SectionsJSON string `json:"sectionsJson" jsonschema_description:"JSON-encoded object mapping section id (e.g. \"version\", \"commit\") to the full field-value object for that section, e.g. {\"version\":{\"mode\":\"file\",\"versionFile\":\"package.json\"}}. A plain top-level id REPLACES that section wholesale. A dotted id (e.g. \"plan.guardrails\") merges at that nested leaf instead, preserving sibling keys under the same top-level section — the leaf itself is still replaced wholesale, not patched. Pass the complete object for the id you name."`
}

// SetupWriteSectionsOut is the output for the setup_write_sections tool.
type SetupWriteSectionsOut struct {
	OK       bool                 `json:"ok"`
	Written  []string             `json:"written"`
	Errors   []string             `json:"errors,omitempty"`
	Scaffold []ScaffoldFileReport `json:"scaffold,omitempty"`
	Warnings []string             `json:"warnings,omitempty"`
}

// RegisterSetupWriteTools registers setup_write_sections on the server.
func RegisterSetupWriteTools(s *mcpserver.Server) {
	mcpserver.Register(s, "setup_write_sections",
		"INTERNAL — called by sdlc skills only. Writes real field-value data into one or more sdlc-v2 config sections (config.toml for project sections, local.toml for local sections), routing and validating via the same config.WriteSection primitive setup_init uses. Unlike setup_init (which writes the full config.toml/local.toml templates verbatim for the user to hand-edit), this accepts the actual assembled values collected during setup's per-section field loop.",
		mcpserver.Annotations{
			Title:       "Write SDLC config sections",
			ReadOnly:    false,
			Destructive: true,
			Idempotent:  false,
			OpenWorld:   false,
		},
		func(ctx mcpserver.Ctx, in SetupWriteSectionsIn) (SetupWriteSectionsOut, error) {
			root, err := worktree.MainRoot()
			if err != nil {
				root, err = os.Getwd()
				if err != nil {
					return SetupWriteSectionsOut{}, &mcpserver.InfraError{
						Msg:        fmt.Sprintf("resolve project root: %s", err.Error()),
						Suggestion: "Restart the sdlc MCP server from a directory that still exists, then retry setup_write_sections.",
						Cause:      err,
					}
				}
			}
			return setupWriteSections(root, in)
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

// wholeNumbersToInt returns v with every float64 that holds a whole number
// replaced by an int64, recursing into maps and slices (maps and slices are
// changed in place). JSON decodes every number as float64, and go-toml
// writes a float64 such as 60 as "60.0", while hand-written config files use
// "60" for integers. A number with a fraction (e.g. 0.5) stays a float64.
func wholeNumbersToInt(v any) any {
	switch val := v.(type) {
	case float64:
		if val == math.Trunc(val) && val >= math.MinInt64 && val < math.MaxInt64 {
			return int64(val)
		}
	case map[string]any:
		for k, e := range val {
			val[k] = wholeNumbersToInt(e)
		}
	case []any:
		for i, e := range val {
			val[i] = wholeNumbersToInt(e)
		}
	}
	return v
}

// sectionFile names the config file a section id is written to.
func sectionFile(id string) string {
	top, _, _ := strings.Cut(id, ".")
	if config.ProjectSections[top] {
		return ".sdlc-v2/config.toml"
	}
	return ".sdlc-v2/local.toml"
}

// setupWriteSections is the core logic, separated from the handler for
// testability.
func setupWriteSections(root string, in SetupWriteSectionsIn) (SetupWriteSectionsOut, error) {
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

	var written []string
	var errs []string
	var rewroteWarnings []string
	for _, id := range ids {
		value := sections[id]
		if value == nil {
			value = map[string]any{}
		}
		value = wholeNumbersToInt(expandDottedKeys(value)).(map[string]any)
		rewrote, err := config.WriteSectionReport(root, id, value)
		if err != nil {
			errs = append(errs, fmt.Sprintf("section %s: %s", id, err.Error()))
			continue
		}
		if rewrote {
			rewroteWarnings = append(rewroteWarnings, fmt.Sprintf(
				"section %s: could not edit %s in place, so the whole file was rewritten and its comments were removed",
				id, sectionFile(id)))
		}
		written = append(written, id)
	}

	out := SetupWriteSectionsOut{OK: len(errs) == 0, Written: written, Warnings: rewroteWarnings}
	if len(errs) > 0 {
		out.Errors = errs
	}

	// Auto-trigger scaffold_ci after the version section is written. This is
	// best-effort: config write is the critical path, so scaffold errors are
	// surfaced as warnings, never as failures.
	for _, id := range written {
		if id == "version" {
			scaffoldOut, err := scaffoldCI(root, false)
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
