// Package tools: dimensions_render_instructions tool (Task 44, Q2).
//
// Ports the CLI entrypoint of scripts/lib/dimension-to-instructions.js
// (`node dimension-to-instructions.js --file <path> [--common-file <path>]`)
// to a Go MCP tool for setup's setup-dimensions.md sub-flow, which
// needs to regenerate a single review dimension's Copilot
// instructions-mirror file after the dimension is installed or edited.
//
// This is a deliberately narrow wrapper: all of the actual transform logic
// (name/triggers/severity/checklist/severity-guide extraction) already
// lives in internal/dimensions.ToInstructions (Task 11's R-copilot-mirror
// port). This file only adds the file I/O the JS CLI wrapper performed —
// read the dimension file (+ optional common-prompt file), call
// ToInstructions, and write the result to
// .github/instructions/<name>.instructions.md — so that setup-dimensions.md
// has an MCP tool to call instead of shelling out to a Node script.
//
// Task 5 (root-rule unification) added a second, unrelated mode selected by
// WriteDimension: persist Content to
// .sdlc-v2/review-dimensions/<Name>.md, so setup-dimensions.md's dimension
// authoring step no longer needs a bare Write to a .sdlc-v2/ path either.
//
// A third mode, ListDimensions (this task), lists the already-installed
// dimension files under .sdlc-v2/review-dimensions/, so setup-dimensions.md's
// Step 2 and setup/SKILL.md's Step 0/Step 3.S snapshot no longer need a bare
// Glob either. It reuses internal/dimensions.Load (the same enumerator
// validate/review already use) rather than a raw filepath.Glob, since Load
// already excludes the shared _common.md common-prompt file and tolerates a
// missing directory.
//
// All three modes default their root to worktree.ActiveRoot(), since
// dimension files are git-tracked content that must follow the active
// worktree (root rule), not the main one.
package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rnagrodzki/sdlc-plugin/internal/dimensions"
	"github.com/rnagrodzki/sdlc-plugin/internal/frontmatter"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
	"github.com/rnagrodzki/sdlc-plugin/internal/worktree"
)

// DimensionsRenderInstructionsIn is the input for the
// dimensions_render_instructions tool.
type DimensionsRenderInstructionsIn struct {
	// File is the review-dimension Markdown file to render (e.g.
	// ".sdlc-v2/review-dimensions/security.md"), relative to the project root
	// unless absolute. Required unless WriteDimension or ListDimensions is
	// true.
	File string `json:"file,omitempty" jsonschema_description:"The review-dimension Markdown file to render (e.g. \".sdlc-v2/review-dimensions/security.md\"), relative to the project root unless absolute. Required unless writeDimension or listDimensions is true."`
	// CommonFile optionally names a shared common-prompt Markdown file
	// (normally ".sdlc-v2/review-dimensions/_common.md") whose trimmed content
	// is injected as the rendered file's "## Common Review Instructions"
	// section, mirroring the JS CLI's optional --common-file flag. Empty
	// omits that section. Relative to the project root unless absolute.
	CommonFile string `json:"commonFile,omitempty" jsonschema_description:"Optional shared common-prompt Markdown file (normally \".sdlc-v2/review-dimensions/_common.md\") whose trimmed content is injected as the rendered file's \"## Common Review Instructions\" section. Empty omits that section. Relative to the project root unless absolute."`
	// ProjectRoot optionally overrides the root all modes resolve against:
	// the write mode's ".sdlc-v2/review-dimensions/<name>.md" destination,
	// the list mode's ".sdlc-v2/review-dimensions/" scan directory, and the
	// render mode's ".github/instructions/" output path (and the
	// File/CommonFile inputs it reads, when relative). Defaults to
	// worktree.ActiveRoot() when empty -- dimension files are git-tracked
	// content, so per the project's root rule they follow the ACTIVE
	// worktree, not the main one, so a dimension added on a branch inside a
	// linked worktree is visible in the same session. harden's
	// Copilot-mirror step (R-copilot-mirror, #474) passes this explicitly
	// from harden_prepare's manifest (repository.contentRoot); that is now
	// redundant with the default but kept as an explicit param.
	ProjectRoot string `json:"projectRoot,omitempty" jsonschema_description:"Overrides the root all modes resolve against (write mode's .sdlc-v2/review-dimensions/ destination, list mode's scan directory, or render mode's .github/instructions/ output path and file/commonFile inputs). Defaults to the active worktree root when empty."`
	// WriteDimension selects write mode: persist Content to
	// ".sdlc-v2/review-dimensions/<Name>.md" under ProjectRoot instead of
	// rendering an existing dimension file to its Copilot instructions
	// mirror. Lets setup-dimensions.md create/update a dimension file
	// through an MCP tool instead of a bare Write to a .sdlc-v2/ path.
	WriteDimension bool `json:"writeDimension,omitempty" jsonschema_description:"Selects write mode: persist content to .sdlc-v2/review-dimensions/<name>.md under projectRoot instead of rendering an existing dimension file. When true, name and content are required and file/commonFile are ignored."`
	// Name is the dimension's file stem (no directory, no .md extension) for
	// write mode, e.g. "security" writes ".sdlc-v2/review-dimensions/security.md".
	// Required when WriteDimension is true. Must not contain a path
	// separator or "..".
	Name string `json:"name,omitempty" jsonschema_description:"Dimension file stem for write mode (no directory, no .md extension, e.g. \"security\"). Required when writeDimension is true. Must not contain a path separator or \"..\"."`
	// Content is the full Markdown content (frontmatter + body) to write for
	// write mode. Required when WriteDimension is true. Not validated by
	// this tool -- run validate({action:"dimensions"}) separately.
	Content string `json:"content,omitempty" jsonschema_description:"Full Markdown content (frontmatter + body) to write for write mode. Required when writeDimension is true. Not validated by this tool -- run validate with action dimensions separately."`
	// ListDimensions selects list mode: return the file-stem names of the
	// dimension files already installed under
	// ".sdlc-v2/review-dimensions/" under ProjectRoot, instead of rendering
	// or writing a dimension file. File/CommonFile/Name/Content are ignored
	// when true. Lets setup-dimensions.md's and setup/SKILL.md's snapshot
	// steps discover installed dimensions through an MCP tool instead of a
	// bare Glob on a .sdlc-v2/ path.
	ListDimensions bool `json:"listDimensions,omitempty" jsonschema_description:"Selects list mode: return the names of dimension files already installed under .sdlc-v2/review-dimensions/ under projectRoot. When true, file/commonFile/name/content are ignored."`
}

// DimensionsRenderInstructionsOut is the output for the
// dimensions_render_instructions tool.
type DimensionsRenderInstructionsOut struct {
	OK   bool   `json:"ok"`
	Path string `json:"path"`
	// Dimensions is list mode's result: the sorted file-stem names (no
	// directory, no .md extension) of the dimension files installed under
	// .sdlc-v2/review-dimensions/, excluding the shared _common.md
	// common-prompt file. Always present (never omitted) and normalized to
	// []string{} rather than nil when there are none, so callers see "[]"
	// rather than "null". Empty on the render/write-mode paths.
	Dimensions []string `json:"dimensions"`
	// Count is list mode's len(Dimensions), included alongside it so callers
	// do not need to count the slice themselves. Zero on the
	// render/write-mode paths.
	Count int `json:"count"`
	// Next is a short instruction for what the caller should do with this
	// result, populated on every mode/path.
	Next string `json:"next"`
}

// RegisterDimensionsRenderTools registers dimensions_render_instructions on
// the server.
func RegisterDimensionsRenderTools(s *mcpserver.Server) {
	mcpserver.Register(s, "dimensions_render_instructions",
		"Renders one review-dimension Markdown file to its GitHub Copilot instructions-mirror at .github/instructions/<name>.instructions.md (R-copilot-mirror; port of scripts/lib/dimension-to-instructions.js's CLI entrypoint), or, with writeDimension:true, writes a new/updated dimension file to .sdlc-v2/review-dimensions/<name>.md, or, with listDimensions:true, lists the dimension file names already installed under .sdlc-v2/review-dimensions/.",
		mcpserver.Annotations{
			Title:       "Render review dimension files",
			ReadOnly:    false,
			Destructive: true,
			Idempotent:  true,
			OpenWorld:   false,
		},
		func(ctx mcpserver.Ctx, in DimensionsRenderInstructionsIn) (DimensionsRenderInstructionsOut, error) {
			root := in.ProjectRoot
			if root == "" {
				var err error
				root, err = worktree.ActiveRoot()
				if err != nil {
					return DimensionsRenderInstructionsOut{}, &mcpserver.InfraError{Msg: fmt.Sprintf("resolve project root: %s", err.Error()), Cause: err}
				}
			}
			return dimensionsRenderInstructions(root, in)
		},
	)
}

// dimensionsRenderInstructions is the core logic, separated from the
// handler for testability.
func dimensionsRenderInstructions(root string, in DimensionsRenderInstructionsIn) (DimensionsRenderInstructionsOut, error) {
	if in.WriteDimension && in.ListDimensions {
		return DimensionsRenderInstructionsOut{}, &mcpserver.DomainError{
			Msg:        "at most one of writeDimension, listDimensions may be true",
			Suggestion: "Set exactly one mode-select field per call; leave the other false or omitted.",
		}
	}
	if in.WriteDimension {
		return writeDimensionFile(root, in)
	}
	if in.ListDimensions {
		return listDimensionFiles(root)
	}
	if in.File == "" {
		return DimensionsRenderInstructionsOut{}, &mcpserver.DomainError{Msg: "dimensions_render_instructions: file is required"}
	}
	filePath := resolvePath(root, in.File)

	content, err := os.ReadFile(filePath)
	if err != nil {
		return DimensionsRenderInstructionsOut{}, &mcpserver.DomainError{
			Msg:   fmt.Sprintf("dimensions_render_instructions: cannot read %s: %s", filePath, err.Error()),
			Cause: err,
		}
	}

	meta, body, err := frontmatter.Parse(content)
	if err != nil {
		return DimensionsRenderInstructionsOut{}, &mcpserver.DomainError{
			Msg:   fmt.Sprintf("dimensions_render_instructions: %s: %s", filePath, err.Error()),
			Cause: err,
		}
	}

	var common string
	if in.CommonFile != "" {
		commonPath := resolvePath(root, in.CommonFile)
		if commonContent, cErr := os.ReadFile(commonPath); cErr == nil {
			common = string(commonContent)
		}
	}

	d := dimensions.Dimension{
		File:   filepath.Base(filePath),
		Meta:   meta,
		Body:   string(body),
		Common: common,
	}

	rendered := dimensions.ToInstructions(d)
	if rendered == "" {
		return DimensionsRenderInstructionsOut{}, &mcpserver.DomainError{
			Msg: fmt.Sprintf("dimensions_render_instructions: %s lacks a usable name or a non-empty triggers list; cannot render", filePath),
		}
	}

	name, _ := meta["name"].(string)
	outPath := filepath.Join(root, ".github", "instructions", name+".instructions.md")

	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return DimensionsRenderInstructionsOut{}, &mcpserver.InfraError{
			Msg:   fmt.Sprintf("create %s: %s", filepath.Dir(outPath), err.Error()),
			Cause: err,
		}
	}
	if err := os.WriteFile(outPath, []byte(rendered), 0o644); err != nil {
		return DimensionsRenderInstructionsOut{}, &mcpserver.InfraError{
			Msg:   fmt.Sprintf("write %s: %s", outPath, err.Error()),
			Cause: err,
		}
	}

	return DimensionsRenderInstructionsOut{
		OK:         true,
		Path:       outPath,
		Dimensions: []string{},
		Next:       fmt.Sprintf("Rendered to %s.", outPath),
	}, nil
}

// listDimensionFiles is list mode's core logic: enumerate the dimension
// files already installed under <root>/.sdlc-v2/review-dimensions/, so
// setup-dimensions.md's and setup/SKILL.md's snapshot steps can discover
// installed dimensions through this tool instead of a bare Glob on a
// .sdlc-v2/ path. Reuses internal/dimensions.Load -- the same enumerator
// validate/review already use -- rather than a raw filepath.Glob, since Load
// already excludes the shared _common.md common-prompt file, sorts the
// result, and tolerates a missing directory (returns nil, nil rather than an
// error) instead of requiring setup to have run first.
func listDimensionFiles(root string) (DimensionsRenderInstructionsOut, error) {
	dir := filepath.Join(root, paths.DataDir, "review-dimensions")
	loaded, err := dimensions.Load(dir)
	if err != nil {
		return DimensionsRenderInstructionsOut{}, &mcpserver.InfraError{
			Msg:        fmt.Sprintf("list %s: %s", dir, err.Error()),
			Suggestion: "Check filesystem permissions on the project root, then retry.",
			Cause:      err,
		}
	}

	names := make([]string, 0, len(loaded))
	for _, d := range loaded {
		names = append(names, strings.TrimSuffix(d.File, ".md"))
	}

	next := fmt.Sprintf("%d review dimension(s) installed under %s/review-dimensions/.", len(names), paths.DataDir)
	if len(names) == 0 {
		next = fmt.Sprintf("No review dimensions installed yet under %s/review-dimensions/.", paths.DataDir)
	}

	return DimensionsRenderInstructionsOut{
		OK:         true,
		Path:       dir,
		Dimensions: names,
		Count:      len(names),
		Next:       next,
	}, nil
}

// writeDimensionFile is write mode's core logic: persist in.Content to
// <root>/.sdlc-v2/review-dimensions/<in.Name>.md, so setup-dimensions.md can
// create or update a dimension file through this tool instead of a bare
// Write to a .sdlc-v2/ path. Frontmatter/body validity is not checked here --
// that is validate({action:"dimensions"})'s job, run separately.
func writeDimensionFile(root string, in DimensionsRenderInstructionsIn) (DimensionsRenderInstructionsOut, error) {
	if in.Name == "" {
		return DimensionsRenderInstructionsOut{}, &mcpserver.DomainError{
			Msg:        "dimensions_render_instructions: name is required when writeDimension is true",
			Suggestion: "Pass a non-empty name (the dimension file stem, e.g. \"security\") when writeDimension is true.",
		}
	}
	if in.Content == "" {
		return DimensionsRenderInstructionsOut{}, &mcpserver.DomainError{
			Msg:        "dimensions_render_instructions: content is required when writeDimension is true",
			Suggestion: "Pass non-empty content (the dimension file's full Markdown) when writeDimension is true.",
		}
	}
	if strings.ContainsAny(in.Name, `/\`) || strings.Contains(in.Name, "..") {
		return DimensionsRenderInstructionsOut{}, &mcpserver.DomainError{
			Msg:        fmt.Sprintf("dimensions_render_instructions: invalid name %q: must not contain a path separator or \"..\"", in.Name),
			Suggestion: "Use a bare filename stem with no path separator or \"..\", e.g. \"security\".",
		}
	}

	dir := filepath.Join(root, paths.DataDir, "review-dimensions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return DimensionsRenderInstructionsOut{}, &mcpserver.InfraError{
			Msg:        fmt.Sprintf("create %s: %s", dir, err.Error()),
			Suggestion: "Check filesystem permissions and available disk space for the project root, then retry.",
			Cause:      err,
		}
	}

	outPath := filepath.Join(dir, in.Name+".md")
	if err := os.WriteFile(outPath, []byte(in.Content), 0o644); err != nil {
		return DimensionsRenderInstructionsOut{}, &mcpserver.InfraError{
			Msg:        fmt.Sprintf("write %s: %s", outPath, err.Error()),
			Suggestion: "Check filesystem permissions and available disk space for the project root, then retry.",
			Cause:      err,
		}
	}

	return DimensionsRenderInstructionsOut{
		OK:         true,
		Path:       outPath,
		Dimensions: []string{},
		Next:       fmt.Sprintf("Written to %s. Run validate({action:\"dimensions\"}) to check frontmatter/body validity.", outPath),
	}, nil
}
