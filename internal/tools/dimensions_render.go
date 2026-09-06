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
package tools

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/rnagrodzki/sdlc-plugin/internal/dimensions"
	"github.com/rnagrodzki/sdlc-plugin/internal/frontmatter"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/worktree"
)

// DimensionsRenderInstructionsIn is the input for the
// dimensions_render_instructions tool.
type DimensionsRenderInstructionsIn struct {
	// File is the review-dimension Markdown file to render (e.g.
	// ".sdlc/review-dimensions/security.md"), relative to the project root
	// unless absolute. Required.
	File string `json:"file"`
	// CommonFile optionally names a shared common-prompt Markdown file
	// (normally ".sdlc/review-dimensions/_common.md") whose trimmed content
	// is injected as the rendered file's "## Common Review Instructions"
	// section, mirroring the JS CLI's optional --common-file flag. Empty
	// omits that section. Relative to the project root unless absolute.
	CommonFile string `json:"commonFile,omitempty"`
	// ProjectRoot optionally overrides the root the ".github/instructions/"
	// output path is written under (and that File/CommonFile resolve
	// against when relative). Defaults to worktree.MainRoot() when empty —
	// correct for setup-dimensions.md's use case (installing into the main
	// worktree). harden's Copilot-mirror step (R-copilot-mirror, #474)
	// needs the mirror written under the ACTIVE worktree instead
	// (repository.contentRoot from harden_prepare's manifest, which may
	// differ from the main worktree), so it passes this explicitly.
	ProjectRoot string `json:"projectRoot,omitempty"`
}

// DimensionsRenderInstructionsOut is the output for the
// dimensions_render_instructions tool.
type DimensionsRenderInstructionsOut struct {
	OK   bool   `json:"ok"`
	Path string `json:"path"`
}

// RegisterDimensionsRenderTools registers dimensions_render_instructions on
// the server.
func RegisterDimensionsRenderTools(s *mcpserver.Server) {
	mcpserver.Register(s, "dimensions_render_instructions",
		"Renders one review-dimension Markdown file to its GitHub Copilot instructions-mirror at .github/instructions/<name>.instructions.md (R-copilot-mirror). Port of scripts/lib/dimension-to-instructions.js's CLI entrypoint.",
		func(ctx mcpserver.Ctx, in DimensionsRenderInstructionsIn) (DimensionsRenderInstructionsOut, error) {
			root := in.ProjectRoot
			if root == "" {
				var err error
				root, err = worktree.MainRoot()
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

	return DimensionsRenderInstructionsOut{OK: true, Path: outPath}, nil
}
