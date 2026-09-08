// Package tools: setup_write_sections tool (Task 44 addition).
//
// Gap this closes: setup_init (Task 26, internal/tools/setup.go) only ever
// seeds EMPTY objects for the section ids it is given — SetupInitIn{Sections
// []string} carries no field values, and config.WriteSection is otherwise
// unexposed to any tool. The source skill's util/setup-init.js took full
// --project-config/--local-config JSON blobs and wrote the user's actual
// answers (e.g. version: {mode: "file", versionFile: "package.json"}). The
// migration plan's Task 26 contract only commits to the empty-scaffold
// behavior ("SetupInitIn{Sections []string} -> created-files report"); it is
// silent on how collected field values reach config.json/local.json.
//
// This is a small additive tool for setup's (Task 44) Step 3 "Writing
// config files" sub-flow, which needs to persist the field values collected
// via AskUserQuestion into the correct section. It does not modify
// setup_init or config.WriteSection — it is a second, narrower caller of the
// same read-merge-write primitive, taking real values instead of {}.
package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"

	"github.com/rnagrodzki/sdlc-plugin/internal/config"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/worktree"
)

// SetupWriteSectionsIn is the input for the setup_write_sections tool.
type SetupWriteSectionsIn struct {
	// SectionsJSON is a JSON-encoded object mapping section id (e.g.
	// "version", "commit") to the full field-value object for that section,
	// e.g. {"version":{"mode":"file","versionFile":"package.json"}}.
	// Each section's value REPLACES the section wholesale (WriteSection
	// semantics) — callers must pass the complete object for a section, not
	// a partial patch.
	SectionsJSON string `json:"sectionsJson"`
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
		"INTERNAL — called by sdlc skills only. Writes real field-value data into one or more sdlc-v2 config sections (config.json for project sections, local.json for local sections), routing and validating via the same config.WriteSection primitive setup_init uses. Unlike setup_init (which only seeds empty {} sections), this accepts the actual assembled values collected during setup's per-section field loop.",
		func(ctx mcpserver.Ctx, in SetupWriteSectionsIn) (SetupWriteSectionsOut, error) {
			root, err := worktree.MainRoot()
			if err != nil {
				root, err = os.Getwd()
				if err != nil {
					return SetupWriteSectionsOut{}, &mcpserver.InfraError{
						Msg:   fmt.Sprintf("resolve project root: %s", err.Error()),
						Cause: err,
					}
				}
			}
			return setupWriteSections(root, in)
		},
	)
}

// setupWriteSections is the core logic, separated from the handler for
// testability.
func setupWriteSections(root string, in SetupWriteSectionsIn) (SetupWriteSectionsOut, error) {
	if in.SectionsJSON == "" {
		return SetupWriteSectionsOut{}, &mcpserver.DomainError{Msg: "setup_write_sections: sectionsJson is required"}
	}

	var sections map[string]map[string]any
	if err := json.Unmarshal([]byte(in.SectionsJSON), &sections); err != nil {
		return SetupWriteSectionsOut{}, &mcpserver.DomainError{
			Msg:   fmt.Sprintf("setup_write_sections: invalid sectionsJson: %s", err.Error()),
			Cause: err,
		}
	}
	if len(sections) == 0 {
		return SetupWriteSectionsOut{}, &mcpserver.DomainError{Msg: "setup_write_sections: sectionsJson must contain at least one section"}
	}

	// Sort ids for deterministic output ordering.
	ids := make([]string, 0, len(sections))
	for id := range sections {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	var written []string
	var errs []string
	for _, id := range ids {
		value := sections[id]
		if value == nil {
			value = map[string]any{}
		}
		if err := config.WriteSection(root, id, value); err != nil {
			errs = append(errs, fmt.Sprintf("section %s: %s", id, err.Error()))
			continue
		}
		written = append(written, id)
	}

	out := SetupWriteSectionsOut{OK: len(errs) == 0, Written: written}
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
