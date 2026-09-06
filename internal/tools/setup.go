package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rnagrodzki/sdlc-plugin/internal/config"
	"github.com/rnagrodzki/sdlc-plugin/internal/configmigrate"
	"github.com/rnagrodzki/sdlc-plugin/internal/execx"
	"github.com/rnagrodzki/sdlc-plugin/internal/fsx"
	"github.com/rnagrodzki/sdlc-plugin/internal/ghx"
	"github.com/rnagrodzki/sdlc-plugin/internal/gitx"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/setupmeta"
	"github.com/rnagrodzki/sdlc-plugin/internal/worktree"
)

// ---------------------------------------------------------------------------
// setup_prepare
// ---------------------------------------------------------------------------

// SetupPrepareIn is the input for the setup_prepare tool.
type SetupPrepareIn struct {
	SkipConfigCheck bool `json:"skipConfigCheck"`
}

// sectionRow is a JSON-friendly projection of setupmeta.Section with
// camelCase tags.
type sectionRow struct {
	ID              string     `json:"id"`
	Label           string     `json:"label"`
	Purpose         string     `json:"purpose"`
	ConfigFile      string     `json:"configFile"`
	ConfigPath      string     `json:"configPath"`
	ConsumedBy      []string   `json:"consumedBy"`
	FilesModified   []string   `json:"filesModified"`
	Optional        bool       `json:"optional"`
	DelegatedTo     string     `json:"delegatedTo,omitempty"`
	ConfirmDetected bool       `json:"confirmDetected"`
	Fields          []fieldRow `json:"fields"`
}

// fieldRow is a JSON-friendly projection of setupmeta.Field with camelCase
// tags.
type fieldRow struct {
	Name                  string   `json:"name"`
	Label                 string   `json:"label"`
	Type                  string   `json:"type"`
	Options               []string `json:"options,omitempty"`
	Default               any      `json:"default,omitempty"`
	Description           string   `json:"description"`
	Min                   *int     `json:"min,omitempty"`
	Max                   *int     `json:"max,omitempty"`
	WhenStepInActiveSteps string   `json:"whenStepInActiveSteps,omitempty"`
}

// SetupPrepareOut is the output for the setup_prepare tool.
type SetupPrepareOut struct {
	OK             bool         `json:"ok"`
	NeedsMigration bool         `json:"needsMigration"`
	Sections       []sectionRow `json:"sections"`
	DefaultBranch  string       `json:"defaultBranch,omitempty"`
	RemoteOwner    string       `json:"remoteOwner,omitempty"`
}

// setupPrepare is the core logic, separated from the handler for testability.
func setupPrepare(root string, in SetupPrepareIn) (SetupPrepareOut, error) {
	// Check migration state (best-effort, never a tool error).
	needsMigration := false
	if !in.SkipConfigCheck {
		if err := configmigrate.Verify(root); err != nil {
			needsMigration = true
		}
	}

	// Convert setupmeta.Sections() to JSON-friendly rows.
	meta := setupmeta.Sections()
	rows := make([]sectionRow, len(meta))
	for i, s := range meta {
		fields := make([]fieldRow, len(s.Fields))
		for j, f := range s.Fields {
			fields[j] = fieldRow{
				Name:                  f.Name,
				Label:                 f.Label,
				Type:                  f.Type,
				Options:               f.Options,
				Default:               f.Default,
				Description:           f.Description,
				Min:                   f.Min,
				Max:                   f.Max,
				WhenStepInActiveSteps: f.WhenStepInActiveSteps,
			}
		}
		rows[i] = sectionRow{
			ID:              s.ID,
			Label:           s.Label,
			Purpose:         s.Purpose,
			ConfigFile:      s.ConfigFile,
			ConfigPath:      s.ConfigPath,
			ConsumedBy:      s.ConsumedBy,
			FilesModified:   s.FilesModified,
			Optional:        s.Optional,
			DelegatedTo:     s.DelegatedTo,
			ConfirmDetected: s.ConfirmDetected,
			Fields:          fields,
		}
	}

	// Best-effort runtime defaults: defaultBranch and remoteOwner.
	defaultBranch, _ := gitx.DefaultBranch(root)
	var remoteOwner string
	originURL, err := execx.Run("git", []string{"remote", "get-url", "origin"}, execx.Options{Dir: root})
	if err == nil && originURL != "" {
		owner, _, parseErr := ghx.ParseRemoteOwner(originURL)
		if parseErr == nil {
			remoteOwner = owner
		}
	}

	return SetupPrepareOut{
		OK:             true,
		NeedsMigration: needsMigration,
		Sections:       rows,
		DefaultBranch:  defaultBranch,
		RemoteOwner:    remoteOwner,
	}, nil
}

// ---------------------------------------------------------------------------
// setup_init
// ---------------------------------------------------------------------------

// SetupInitIn is the input for the setup_init tool.
type SetupInitIn struct {
	Sections []string `json:"sections"`
}

// SetupInitOut is the output for the setup_init tool.
type SetupInitOut struct {
	OK      bool     `json:"ok"`
	Created []string `json:"created"`
	Changed []string `json:"changed"`
	Errors  []string `json:"errors,omitempty"`
}

// Managed-block markers for .sdlc/.gitignore.
const (
	sdlcGitignoreBegin = "# >>> sdlc-utilities managed (do not edit) — selective ignores"
	sdlcGitignoreEnd   = "# <<< sdlc-utilities managed"
)

// sdlcGitignorePatterns are the deny-all + allowlist patterns inside
// .sdlc/.gitignore, mirroring SDLC_GITIGNORE_PATTERNS from the JS source.
var sdlcGitignorePatterns = []string{
	"*",
	"!.gitignore",
	"!config.json",
	"!review-dimensions/",
	"!review-dimensions/**",
}

// Managed-block markers for root .gitignore (v3).
const (
	rootGitignoreBegin   = "# >>> sdlc-utilities managed v3 (do not edit) — transient skill artifacts"
	rootGitignoreEnd     = "# <<< sdlc-utilities managed"
	rootGitignoreBeginV2 = "# >>> sdlc-utilities managed v2 (do not edit) — transient skill artifacts and .sdlc/ runtime"
	rootGitignoreBeginV1 = "# >>> sdlc-utilities managed (do not edit) — transient skill artifacts"
)

// rootGitignorePatterns are the transient-artifact glob families managed
// in the consumer project root .gitignore.
var rootGitignorePatterns = []string{
	"*-context-*.json",
	"*-manifest-*.json",
	"*-prepare-*.json",
}

// ensureManagedBlock reads a gitignore file, strips any existing managed
// block (identified by beginMarker/endMarker or legacy markers), appends the
// new managed block, and writes the result. Returns "created", "updated", or
// "unchanged".
func ensureManagedBlock(path, beginMarker, endMarker string, patterns []string, legacyBeginMarkers []string) (string, error) {
	managedBlock := beginMarker + "\n" + strings.Join(patterns, "\n") + "\n" + endMarker

	existing := ""
	fileExisted := false
	data, err := os.ReadFile(path)
	if err == nil {
		existing = string(data)
		fileExisted = true
	}

	// Split into lines; strip trailing empty line from final newline.
	var lines []string
	if existing != "" {
		lines = strings.Split(existing, "\n")
		if len(lines) > 0 && lines[len(lines)-1] == "" {
			lines = lines[:len(lines)-1]
		}
	}

	// Build set of all begin markers (current + legacy) for removal.
	beginMarkers := map[string]bool{beginMarker: true}
	for _, m := range legacyBeginMarkers {
		beginMarkers[m] = true
	}

	// Build a set of managed patterns for legacy raw-pattern removal.
	patternSet := make(map[string]bool, len(patterns))
	for _, p := range patterns {
		patternSet[p] = true
	}

	// Remove existing managed blocks and legacy raw patterns.
	var otherLines []string
	insideBlock := false
	for _, line := range lines {
		if beginMarkers[line] {
			insideBlock = true
			continue
		}
		if line == endMarker {
			insideBlock = false
			continue
		}
		if insideBlock {
			continue
		}
		// Drop "other" lines matching managed patterns (legacy raw lines).
		if patternSet[strings.TrimSpace(line)] {
			continue
		}
		otherLines = append(otherLines, line)
	}

	// Normalize blank lines: trim leading/trailing, collapse consecutive.
	otherLines = normalizeBlankLines(otherLines)

	// Reconstruct: user lines + managed block.
	var next string
	if len(otherLines) > 0 {
		next = strings.Join(otherLines, "\n") + "\n" + managedBlock + "\n"
	} else {
		next = managedBlock + "\n"
	}

	if next == existing {
		return "unchanged", nil
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(next), 0o644); err != nil {
		return "", fmt.Errorf("write %s: %w", path, err)
	}

	if fileExisted {
		return "updated", nil
	}
	return "created", nil
}

// normalizeBlankLines trims leading/trailing blanks and collapses consecutive
// blank lines to at most one. Mirrors the JS normalizeBlankLines helper.
func normalizeBlankLines(lines []string) []string {
	if len(lines) == 0 {
		return nil
	}
	isBlank := func(s string) bool {
		return strings.TrimSpace(s) == ""
	}

	// Trim leading blanks.
	start := 0
	for start < len(lines) && isBlank(lines[start]) {
		start++
	}
	// Trim trailing blanks.
	end := len(lines) - 1
	for end >= start && isBlank(lines[end]) {
		end--
	}
	if end < start {
		return nil
	}

	var out []string
	prevBlank := false
	for i := start; i <= end; i++ {
		blank := isBlank(lines[i])
		if blank {
			if prevBlank {
				continue
			}
			out = append(out, "")
			prevBlank = true
		} else {
			out = append(out, lines[i])
			prevBlank = false
		}
	}
	return out
}

// setupInit is the core logic, separated from the handler for testability.
func setupInit(root string, in SetupInitIn) (SetupInitOut, error) {
	var created []string
	var changed []string
	var errs []string

	sdlcDir := filepath.Join(root, ".sdlc")

	// 1. Create .sdlc/ directory.
	if err := os.MkdirAll(sdlcDir, 0o755); err != nil {
		return SetupInitOut{}, &mcpserver.InfraError{
			Msg:   fmt.Sprintf("create .sdlc directory: %s", err.Error()),
			Cause: err,
		}
	}

	// 2. Ensure .sdlc/.gitignore with managed block.
	sdlcGitignorePath := filepath.Join(sdlcDir, ".gitignore")
	action, err := ensureManagedBlock(
		sdlcGitignorePath,
		sdlcGitignoreBegin,
		sdlcGitignoreEnd,
		sdlcGitignorePatterns,
		nil,
	)
	if err != nil {
		errs = append(errs, fmt.Sprintf(".sdlc/.gitignore: %s", err.Error()))
	} else {
		switch action {
		case "created":
			created = append(created, ".sdlc/.gitignore")
		case "updated":
			changed = append(changed, ".sdlc/.gitignore")
		}
	}

	// 3. Ensure root .gitignore with managed block.
	rootGitignorePath := filepath.Join(root, ".gitignore")
	action, err = ensureManagedBlock(
		rootGitignorePath,
		rootGitignoreBegin,
		rootGitignoreEnd,
		rootGitignorePatterns,
		[]string{rootGitignoreBeginV1, rootGitignoreBeginV2},
	)
	if err != nil {
		errs = append(errs, fmt.Sprintf(".gitignore: %s", err.Error()))
	} else {
		switch action {
		case "created":
			created = append(created, ".gitignore")
		case "updated":
			changed = append(changed, ".gitignore")
		}
	}

	// 4. Seed config.json and local.json with empty sections for selected
	//    sections. Uses config.WriteSection for each to get correct routing.
	projectNeeded := false
	localNeeded := false
	for _, sec := range in.Sections {
		if config.ProjectSections[sec] {
			projectNeeded = true
		} else {
			localNeeded = true
		}
	}

	// Ensure config.json exists (even if empty) when any project section or
	// no sections are selected — setup always creates the scaffold.
	configPath := filepath.Join(sdlcDir, "config.json")
	localPath := filepath.Join(sdlcDir, "local.json")

	if projectNeeded || len(in.Sections) == 0 {
		if wasCreated, err := ensureJSONFile(configPath); err != nil {
			errs = append(errs, fmt.Sprintf("config.json: %s", err.Error()))
		} else if wasCreated {
			created = appendIfNew(created, ".sdlc/config.json")
		}
	}
	if localNeeded || len(in.Sections) == 0 {
		if wasCreated, err := ensureJSONFile(localPath); err != nil {
			errs = append(errs, fmt.Sprintf("local.json: %s", err.Error()))
		} else if wasCreated {
			created = appendIfNew(created, ".sdlc/local.json")
		}
	}

	// Write empty objects for each selected section.
	for _, sec := range in.Sections {
		if err := config.WriteSection(root, sec, map[string]any{}); err != nil {
			errs = append(errs, fmt.Sprintf("section %s: %s", sec, err.Error()))
		}
	}

	out := SetupInitOut{
		OK:      len(errs) == 0,
		Created: created,
		Changed: changed,
	}
	if len(errs) > 0 {
		out.Errors = errs
	}
	return out, nil
}

// ensureJSONFile creates a JSON file with an empty object if it does not
// exist. Returns (true, nil) when the file was created, (false, nil) when
// it already existed.
func ensureJSONFile(path string) (bool, error) {
	if _, err := os.Stat(path); err == nil {
		return false, nil // already exists
	}
	return true, fsx.AtomicWriteJSON(path, map[string]any{})
}

// appendIfNew appends s to slice only if not already present.
func appendIfNew(slice []string, s string) []string {
	for _, v := range slice {
		if v == s {
			return slice
		}
	}
	return append(slice, s)
}

// ---------------------------------------------------------------------------
// Registration
// ---------------------------------------------------------------------------

// RegisterSetupTools registers setup_prepare and setup_init on the server.
func RegisterSetupTools(s *mcpserver.Server) {
	mcpserver.Register(s, "setup_prepare",
		"Returns the canonical section descriptors for setup-sdlc, with per-section field metadata and runtime-detected defaults (defaultBranch, remoteOwner). Optionally checks config migration state.",
		func(ctx mcpserver.Ctx, in SetupPrepareIn) (SetupPrepareOut, error) {
			root, err := worktree.MainRoot()
			if err != nil {
				// Fallback to cwd — setup must work before any context exists.
				root, err = os.Getwd()
				if err != nil {
					return SetupPrepareOut{}, &mcpserver.InfraError{
						Msg:   fmt.Sprintf("resolve project root: %s", err.Error()),
						Cause: err,
					}
				}
			}
			return setupPrepare(root, in)
		},
	)

	mcpserver.Register(s, "setup_init",
		"Creates the .sdlc/ directory scaffold for a v5 config: .sdlc/.gitignore, root .gitignore managed block, config.json, and local.json. Seeds empty objects for selected sections.",
		func(ctx mcpserver.Ctx, in SetupInitIn) (SetupInitOut, error) {
			root, err := worktree.MainRoot()
			if err != nil {
				root, err = os.Getwd()
				if err != nil {
					return SetupInitOut{}, &mcpserver.InfraError{
						Msg:   fmt.Sprintf("resolve project root: %s", err.Error()),
						Cause: err,
					}
				}
			}
			return setupInit(root, in)
		},
	)
}
