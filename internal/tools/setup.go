package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rnagrodzki/sdlc-plugin/internal/configmigrate"
	"github.com/rnagrodzki/sdlc-plugin/internal/execx"
	"github.com/rnagrodzki/sdlc-plugin/internal/ghx"
	"github.com/rnagrodzki/sdlc-plugin/internal/gitx"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
	"github.com/rnagrodzki/sdlc-plugin/internal/setupmeta"
	"github.com/rnagrodzki/sdlc-plugin/internal/worktree"

	version "github.com/rnagrodzki/sdlc-plugin"
)

// ---------------------------------------------------------------------------
// setup_prepare
// ---------------------------------------------------------------------------

// SetupPrepareIn is the input for the setup_prepare tool.
type SetupPrepareIn struct {
	SkipConfigCheck bool `json:"skipConfigCheck,omitempty" jsonschema_description:"Skips the config-version auto-migration gate normally run before preflight checks. Set only when the caller has already verified or migrated the config."`
	// Explain selects explain mode: when set, setupPrepareWithDrift
	// returns only the explanation of the named option (see
	// optionExplanation) instead of the full section list. Checked before
	// any other work so the reply stays small during setup.
	Explain string `json:"explain,omitempty" jsonschema_description:"Plain text. The option to explain, as <sectionId>.<fieldName> from sections[].id and sections[].fields[].name. When set, the result holds only the explanation. Example: ship.rebase"`
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
	// Examples holds 1 to 3 sample values for this field, mirroring
	// setupmeta.Field.Examples (see explain mode on setup_prepare).
	Examples []string `json:"examples,omitempty"`
}

// optionExplanation is explain mode's result: the full explanation of one
// setupmeta field, named "<sectionId>.<fieldName>". Deliberately omits a
// "current value" — setup_prepare has no config-reading role (Deviations,
// "Current value in explain").
type optionExplanation struct {
	Option      string   `json:"option"`
	Label       string   `json:"label"`
	Type        string   `json:"type"`
	Options     []string `json:"options"`
	Default     any      `json:"default"`
	Description string   `json:"description"`
	Details     string   `json:"details"`
	Examples    []string `json:"examples"`
	ConfigFile  string   `json:"configFile"`
	ConfigPath  string   `json:"configPath"`
	ConsumedBy  []string `json:"consumedBy"`
}

// SetupPrepareOut is the output for the setup_prepare tool.
type SetupPrepareOut struct {
	OK             bool         `json:"ok"`
	NeedsMigration bool         `json:"needsMigration"`
	Sections       []sectionRow `json:"sections"`
	DefaultBranch  string       `json:"defaultBranch,omitempty"`
	RemoteOwner    string       `json:"remoteOwner,omitempty"`
	// CIScriptDrift compares each scaffold_ci-managed script/workflow
	// against the version currently embedded in this binary (Task 3, R2).
	// Best-effort: degrades to an empty list rather than failing
	// setup_prepare if the comparison errors.
	CIScriptDrift []CIScriptDriftEntry `json:"ciScriptDrift"`
	// Explanation is explain mode's result (see SetupPrepareIn.Explain).
	// Set only in explain mode, where it is the only populated field
	// besides OK and Next.
	Explanation *optionExplanation `json:"explanation,omitempty"`
	// Next is explain mode's instruction to the calling skill: show the
	// explanation, then ask the open question again. Empty outside
	// explain mode.
	Next string `json:"next,omitempty"`
}

// setupPrepareExplainOut is the wire shape for explain mode: only ok,
// explanation and next (spec.md "Output fields": "With explain, the result
// holds only ok, explanation and next"). SetupPrepareOut itself cannot drop
// NeedsMigration/Sections/CIScriptDrift via a json:",omitempty" tag change,
// because those three fields must stay present (and not disappear on a
// false/empty/zero value) in normal mode -- see the same fields' table rows.
// A custom MarshalJSON on SetupPrepareOut would not help either: the actual
// MCP wire format is not JSON. mcpserver.Register renders tool output as
// Markdown via renderOK, which walks the returned value's fields by
// reflection and reads the "json" struct tag directly for names/omitempty
// (mcpserver/render.go's structEntries) -- it never calls MarshalJSON. So the
// only way to drop fields from what the caller actually sees is to hand
// renderOK a value whose type does not have them, which is what
// setupPrepareResult does for the registered handler.
type setupPrepareExplainOut struct {
	OK          bool               `json:"ok"`
	Explanation *optionExplanation `json:"explanation"`
	Next        string             `json:"next"`
}

// setupPrepareResult projects a SetupPrepareOut down to setupPrepareExplainOut
// when it is explain mode's result (Explanation set -- see
// SetupPrepareOut.Explanation's comment), so the registered setup_prepare
// handler hands renderOK a value that carries only ok/explanation/next.
// Normal-mode results pass through unchanged, including the "always present"
// needsMigration/sections/ciScriptDrift fields their table rows require.
func setupPrepareResult(out SetupPrepareOut) any {
	if out.Explanation != nil {
		return setupPrepareExplainOut{OK: out.OK, Explanation: out.Explanation, Next: out.Next}
	}
	return out
}

// setupPrepare is the core logic, separated from the handler for
// testability. It reads CI script drift from root itself — correct for
// tests and any other caller outside a worktree. The registered handler
// instead calls setupPrepareWithDrift so the drift comparison reads the
// active worktree, where scaffold_ci writes (see worktree.ActiveRoot).
func setupPrepare(root string, in SetupPrepareIn) (SetupPrepareOut, error) {
	return setupPrepareWithDrift(root, root, in)
}

// setupPrepareWithDrift is setupPrepare with an explicit driftRoot: config,
// sections, and git metadata are read from root, while CI script drift is
// compared against driftRoot. Kept separate from root so the registered
// handler can point driftRoot at the active worktree without disturbing the
// main-worktree anchoring every other part of setup_prepare relies on.
func setupPrepareWithDrift(root, driftRoot string, in SetupPrepareIn) (SetupPrepareOut, error) {
	// Explain mode is checked before any other work, so the reply stays
	// small during setup and never pays for migration/CI-drift checks it
	// does not need.
	if in.Explain != "" {
		return setupExplainOption(in.Explain)
	}

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
				Examples:              f.Examples,
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

	// Best-effort CI script drift comparison (Task 3, R2): never fails
	// setup_prepare, degrades to an empty list on error.
	ciDrift, driftErr := ciScriptDrift(driftRoot)
	if driftErr != nil {
		ciDrift = nil
	}
	if ciDrift == nil {
		ciDrift = []CIScriptDriftEntry{}
	}

	return SetupPrepareOut{
		OK:             true,
		NeedsMigration: needsMigration,
		Sections:       rows,
		DefaultBranch:  defaultBranch,
		RemoteOwner:    remoteOwner,
		CIScriptDrift:  ciDrift,
	}, nil
}

// setupExplainOption implements setup_prepare's explain mode: it parses
// explain as "<sectionId>.<fieldName>" (split at the first "."), looks up
// that field in setupmeta.Sections(), and returns only its explanation. See
// SetupPrepareIn.Explain and optionExplanation.
func setupExplainOption(explain string) (SetupPrepareOut, error) {
	sectionID, fieldName, ok := strings.Cut(explain, ".")
	if !ok || sectionID == "" || fieldName == "" {
		return SetupPrepareOut{}, &mcpserver.DomainError{
			Msg:        fmt.Sprintf(`setup_prepare: explain %q is not <sectionId>.<fieldName>`, explain),
			Suggestion: `Pass a value such as "ship.rebase".`,
		}
	}

	meta := setupmeta.Sections()
	var section *setupmeta.Section
	for i := range meta {
		if meta[i].ID == sectionID {
			section = &meta[i]
			break
		}
	}
	if section == nil {
		ids := make([]string, len(meta))
		for i, s := range meta {
			ids[i] = s.ID
		}
		return SetupPrepareOut{}, &mcpserver.DomainError{
			Msg:        fmt.Sprintf("setup_prepare: unknown section %q; valid: %v", sectionID, ids),
			Suggestion: "Use a section id from sections[].id.",
		}
	}

	if len(section.Fields) == 0 {
		return SetupPrepareOut{}, &mcpserver.DomainError{
			Msg:        fmt.Sprintf("setup_prepare: section %q has no fields; it runs the %s sub-flow", sectionID, section.DelegatedTo),
			Suggestion: "Explain from that sub-flow file instead.",
		}
	}

	var field *setupmeta.Field
	for i := range section.Fields {
		if section.Fields[i].Name == fieldName {
			field = &section.Fields[i]
			break
		}
	}
	if field == nil {
		names := make([]string, len(section.Fields))
		for i, f := range section.Fields {
			names[i] = f.Name
		}
		return SetupPrepareOut{}, &mcpserver.DomainError{
			Msg:        fmt.Sprintf("setup_prepare: section %q has no field %q; valid: %v", sectionID, fieldName, names),
			Suggestion: "Use a name from sections[].fields[].name.",
		}
	}

	return SetupPrepareOut{
		OK: true,
		Explanation: &optionExplanation{
			Option:      sectionID + "." + fieldName,
			Label:       field.Label,
			Type:        field.Type,
			Options:     field.Options,
			Default:     field.Default,
			Description: field.Description,
			Details:     field.Details,
			Examples:    field.Examples,
			ConfigFile:  section.ConfigFile,
			ConfigPath:  section.ConfigPath,
			ConsumedBy:  section.ConsumedBy,
		},
		Next: "Show the explanation to the user, then ask the open question again.",
	}, nil
}

// boolCount returns how many of the given booleans are true. Used to reject
// ambiguous multi-mode-select input (e.g. two mutually exclusive "select
// this mode" fields both set to true) instead of silently picking the first
// one in dispatch order.
func boolCount(bs ...bool) int {
	n := 0
	for _, b := range bs {
		if b {
			n++
		}
	}
	return n
}

// ---------------------------------------------------------------------------
// setup_init
// ---------------------------------------------------------------------------

// SetupInitIn is the input for the setup_init tool. With no fields set,
// setup_init always writes the complete config.toml/local.toml templates
// (every field, heavily commented) rather than seeding a caller-selected
// subset of sections — see configTemplate/localTemplate below. The
// WritePlanTemplate/WritePRTemplate fields select two unrelated write
// modes (Task 6) that skip that scaffold entirely. CheckPlanTemplate,
// CheckPRTemplate, and ReadPlanTemplate (this task) select three more,
// all read-only, so setup's skills never need a bare Glob/Read on a
// .sdlc-v2/ path to check for or show these two files. Mode fields are
// mutually exclusive: setupInit rejects a call with more than one set to
// true, instead of silently picking one by dispatch order.
type SetupInitIn struct {
	// WritePlanTemplate selects write mode: copy the shipped
	// plan-template-default.md byte-for-byte to .sdlc-v2/plan-template.md
	// instead of scaffolding config.toml/local.toml. Lets
	// setup-plan-template.md's Step 2 create the project's plan template
	// through this tool instead of a bare `cp` to a .sdlc-v2/ path.
	WritePlanTemplate bool `json:"writePlanTemplate,omitempty" jsonschema_description:"Selects write mode: copy the shipped plan-template-default.md byte-for-byte to .sdlc-v2/plan-template.md instead of scaffolding config.toml/local.toml. When true, all other setup_init behavior is skipped."`
	// WritePRTemplate selects write mode: persist Content verbatim to
	// .sdlc-v2/pr-template.md instead of scaffolding
	// config.toml/local.toml. Lets setup-pr-template.md's Step 6 write
	// the accepted PR template through this tool instead of a bare Write
	// to a .sdlc-v2/ path.
	WritePRTemplate bool `json:"writePRTemplate,omitempty" jsonschema_description:"Selects write mode: persist content verbatim to .sdlc-v2/pr-template.md instead of scaffolding config.toml/local.toml. Requires content. When true, all other setup_init behavior is skipped."`
	// Content is the full PR template Markdown to write for
	// WritePRTemplate mode. Required when WritePRTemplate is true;
	// ignored otherwise.
	Content string `json:"content,omitempty" jsonschema_description:"Full PR template Markdown to write verbatim for write-PR-template mode. Required when writePRTemplate is true."`
	// CheckPlanTemplate selects check mode: report whether
	// .sdlc-v2/plan-template.md exists, without reading its content. Lets
	// setup/SKILL.md's snapshot steps check for the file through this tool
	// instead of a bare Glob on a .sdlc-v2/ path.
	CheckPlanTemplate bool `json:"checkPlanTemplate,omitempty" jsonschema_description:"Selects check mode: report whether .sdlc-v2/plan-template.md exists (out.exists), without reading its content. When true, all other setup_init behavior is skipped."`
	// CheckPRTemplate selects check mode: report whether
	// .sdlc-v2/pr-template.md exists, without reading its content. Lets
	// setup/SKILL.md's snapshot steps check for the file through this tool
	// instead of a bare Glob on a .sdlc-v2/ path.
	CheckPRTemplate bool `json:"checkPRTemplate,omitempty" jsonschema_description:"Selects check mode: report whether .sdlc-v2/pr-template.md exists (out.exists), without reading its content. When true, all other setup_init behavior is skipped."`
	// ReadPlanTemplate selects read mode: report whether
	// .sdlc-v2/plan-template.md exists and, when it does, its full content.
	// Lets setup-plan-template.md's Steps 1 and 3 show the file's content
	// through this tool instead of a bare Read on a .sdlc-v2/ path.
	ReadPlanTemplate bool `json:"readPlanTemplate,omitempty" jsonschema_description:"Selects read mode: report whether .sdlc-v2/plan-template.md exists (out.exists) and, when it does, its full content (out.content, empty string otherwise). When true, all other setup_init behavior is skipped."`
}

// SetupInitOut is the output for the setup_init tool.
type SetupInitOut struct {
	OK bool `json:"ok"`
	// Root is the absolute path of the active git worktree that
	// git-tracked files (config.toml, both .gitignore blocks,
	// plan-template.md, pr-template.md) were written under.
	Root    string   `json:"root" jsonschema_description:"Absolute path of the active git worktree that git-tracked files (config.toml, both .gitignore blocks, plan-template.md, pr-template.md) were written under."`
	Created []string `json:"created"`
	Changed []string `json:"changed"`
	Next    string   `json:"next"`
	Errors  []string `json:"errors,omitempty"`
	// Exists is check/read mode's result: whether the checked/read file
	// exists. Always present (never omitted) on those paths so callers see
	// an explicit "false" rather than a missing key; false and unset on the
	// scaffold/write-mode paths.
	Exists bool `json:"exists"`
	// Content is read mode's result: the full content of
	// .sdlc-v2/plan-template.md when it exists, or "" when it does not.
	// Always present (never omitted); empty on every other path.
	Content string `json:"content"`
}

// Managed-block markers for .sdlc-v2/.gitignore.
const (
	sdlcGitignoreBegin = "# >>> sdlc-v2 managed (do not edit) — selective ignores"
	sdlcGitignoreEnd   = "# <<< sdlc-v2 managed"
)

// CommittableStateEntry describes one entry inside .sdlc-v2/ that is
// legitimately tracked in git, in every worktree.
type CommittableStateEntry struct {
	Name  string
	IsDir bool
}

// CommittableStateDirEntries is the canonical allowlist of entries inside
// .sdlc-v2/ that are committed to git rather than being worktree-local
// state (the "committable set" underpinning the root rule in the worktree
// anchoring design). Both the .gitignore managed block below
// (sdlcGitignorePatterns) and worktree_anchoring's stray-state check
// (findStrayStateEntries in validators.go) derive their allowlists from
// this single source so the two cannot drift apart.
var CommittableStateDirEntries = []CommittableStateEntry{
	{Name: paths.GitignoreFile},
	{Name: paths.ConfigFile},
	{Name: paths.ReviewDimensionsSubdir, IsDir: true},
}

// sdlcGitignorePatterns are the deny-all + allowlist patterns inside
// .sdlc-v2/.gitignore, mirroring SDLC_GITIGNORE_PATTERNS from the JS source.
var sdlcGitignorePatterns = buildSdlcGitignorePatterns(CommittableStateDirEntries)

// buildSdlcGitignorePatterns turns the committable-entry allowlist into
// gitignore deny-all + allowlist syntax: a directory entry needs both
// "!name/" (to unignore the directory itself) and "!name/**" (to unignore
// its contents), a file entry needs only "!name".
func buildSdlcGitignorePatterns(entries []CommittableStateEntry) []string {
	patterns := []string{"*"}
	for _, e := range entries {
		if e.IsDir {
			patterns = append(patterns, "!"+e.Name+"/", "!"+e.Name+"/**")
		} else {
			patterns = append(patterns, "!"+e.Name)
		}
	}
	return patterns
}

// Managed-block markers for root .gitignore (v3).
const (
	rootGitignoreBegin   = "# >>> sdlc-v2 managed v3 (do not edit) — transient skill artifacts"
	rootGitignoreEnd     = "# <<< sdlc-v2 managed"
	rootGitignoreBeginV2 = "# >>> sdlc-v2 managed v2 (do not edit) — transient skill artifacts and .sdlc/ runtime"
	rootGitignoreBeginV1 = "# >>> sdlc-v2 managed (do not edit) — transient skill artifacts"
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

// configTemplate and localTemplate hold the complete .sdlc-v2/config.toml
// and local.toml scaffolds written verbatim by setup_init. The canonical,
// browsable source files live at plugins/sdlc/templates/config.toml and
// local.toml; version.ConfigTemplate/LocalTemplate embed them at compile
// time (go:embed can't reach outside internal/tools's own directory
// subtree, so the go:embed directives live in the repo-root version
// package instead — see configtemplates.go). jira is left uncommented
// (with empty string values) in config.toml rather than commented out:
// TestConfigTemplateKeysMatchWhitelist requires every AllowedProjectKeys
// entry to actually parse out of the template, not just be mentioned in a
// comment.
var (
	configTemplate = version.ConfigTemplate
	localTemplate  = version.LocalTemplate
)

// setupInit keeps the single-root form for tests and non-worktree callers.
func setupInit(root string, in SetupInitIn) (SetupInitOut, error) {
	return setupInitRoots(root, root, in)
}

// setupInitRoots is the core logic, separated from the handler for
// testability. Git-tracked files (config.toml, both .gitignore blocks,
// plan-template.md, pr-template.md) are written under contentRoot — the
// active git worktree — while gitignored state (.sdlc-v2/runs/,
// local.toml) is written under stateRoot — the main worktree. In the
// common case (no linked worktree in use) contentRoot and stateRoot are
// the same path.
func setupInitRoots(contentRoot, stateRoot string, in SetupInitIn) (SetupInitOut, error) {
	if n := boolCount(in.WritePlanTemplate, in.WritePRTemplate, in.CheckPlanTemplate, in.CheckPRTemplate, in.ReadPlanTemplate); n > 1 {
		return SetupInitOut{}, &mcpserver.DomainError{
			Msg:        "at most one of writePlanTemplate, writePRTemplate, checkPlanTemplate, checkPRTemplate, readPlanTemplate may be true",
			Suggestion: "Set exactly one mode-select field per call; leave the rest false or omitted.",
		}
	}
	if in.WritePlanTemplate {
		out, err := setupWritePlanTemplate(contentRoot)
		out.Root = contentRoot
		return out, err
	}
	if in.WritePRTemplate {
		out, err := setupWritePRTemplate(contentRoot, in)
		out.Root = contentRoot
		return out, err
	}
	if in.CheckPlanTemplate {
		out, err := setupCheckTemplateExists(contentRoot, paths.PlanTemplateFile)
		out.Root = contentRoot
		return out, err
	}
	if in.CheckPRTemplate {
		out, err := setupCheckTemplateExists(contentRoot, paths.PRTemplateFile)
		out.Root = contentRoot
		return out, err
	}
	if in.ReadPlanTemplate {
		out, err := setupReadPlanTemplate(contentRoot)
		out.Root = contentRoot
		return out, err
	}

	created := []string{}
	changed := []string{}
	var errs []string

	sdlcDir := filepath.Join(contentRoot, paths.DataDir)
	stateDir := filepath.Join(stateRoot, paths.DataDir)

	// 1. Create .sdlc-v2/ directory under both roots (a no-op on the root
	// that's already created when contentRoot == stateRoot).
	for _, d := range []string{sdlcDir, stateDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return SetupInitOut{}, &mcpserver.InfraError{
				Msg:        fmt.Sprintf("create %s directory: %s", paths.DataDir, err.Error()),
				Suggestion: "Check write permission on the project root and free disk space, then retry setup_init.",
				Cause:      err,
			}
		}
	}

	// 1b. Create .sdlc-v2/runs/ directory under the state root. state.Init
	// also creates this lazily on first run, but scaffolding it eagerly
	// here makes it discoverable right after setup, matching the other
	// managed subdirectories setup owns (e.g. review-dimensions/).
	if err := os.MkdirAll(filepath.Join(stateDir, paths.RunsSubdir), 0o755); err != nil {
		return SetupInitOut{}, &mcpserver.InfraError{
			Msg:        fmt.Sprintf("create %s/%s directory: %s", paths.DataDir, paths.RunsSubdir, err.Error()),
			Suggestion: "Check write permission on " + paths.DataDir + " and free disk space, then retry setup_init.",
			Cause:      err,
		}
	}

	// 2. Ensure .sdlc-v2/.gitignore with managed block.
	sdlcGitignorePath := filepath.Join(sdlcDir, paths.GitignoreFile)
	action, err := ensureManagedBlock(
		sdlcGitignorePath,
		sdlcGitignoreBegin,
		sdlcGitignoreEnd,
		sdlcGitignorePatterns,
		nil,
	)
	if err != nil {
		errs = append(errs, fmt.Sprintf("%s/.gitignore: %s", paths.DataDir, err.Error()))
	} else {
		switch action {
		case "created":
			created = append(created, paths.DataDir+"/.gitignore")
		case "updated":
			changed = append(changed, paths.DataDir+"/.gitignore")
		}
	}

	// 3. Ensure root .gitignore with managed block.
	rootGitignorePath := filepath.Join(contentRoot, ".gitignore") // project-root .gitignore, not a paths.* DataDir entry
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
			created = append(created, ".gitignore") // project-root .gitignore, not a paths.* DataDir entry
		case "updated":
			changed = append(changed, ".gitignore") // project-root .gitignore, not a paths.* DataDir entry
		}
	}

	// 4. Write the complete config.toml/local.toml templates directly to
	//    disk — never through LLM context. Every field, with inline
	//    documentation, is dropped in one shot; there is no more
	//    per-section interactive seeding. Idempotent: an existing file is
	//    left untouched so a re-run of /setup never clobbers a user's
	//    edits.
	configPath := filepath.Join(sdlcDir, paths.ConfigFile)
	if _, statErr := os.Stat(configPath); statErr != nil {
		if err := os.WriteFile(configPath, []byte(configTemplate), 0o644); err != nil {
			errs = append(errs, fmt.Sprintf("config.toml: %s", err.Error()))
		} else {
			created = appendIfNew(created, paths.DataDir+"/config.toml")
		}
	}

	localPath := filepath.Join(stateDir, paths.LocalConfigFile)
	if _, statErr := os.Stat(localPath); statErr != nil {
		if err := os.WriteFile(localPath, []byte(localTemplate), 0o644); err != nil {
			errs = append(errs, fmt.Sprintf("local.toml: %s", err.Error()))
		} else {
			created = appendIfNew(created, paths.DataDir+"/local.toml")
		}
	}

	// 5. Clean up stale JSON-era config files. Runs unconditionally (not
	// gated on whether the TOML above was just created), so a stale
	// config.json/local.json left behind from before the TOML migration
	// gets swept up on any /setup re-run. Rename rather than delete to
	// preserve user data; skip if a .bak already exists so a second run
	// never overwrites an earlier backup.
	for _, c := range []struct{ base, dir string }{{"config", sdlcDir}, {"local", stateDir}} {
		jsonPath := filepath.Join(c.dir, c.base+".json")
		bakPath := filepath.Join(c.dir, c.base+".json.bak")
		if _, statErr := os.Stat(jsonPath); statErr == nil {
			if _, statErr := os.Stat(bakPath); statErr != nil {
				if renameErr := os.Rename(jsonPath, bakPath); renameErr != nil {
					errs = append(errs, fmt.Sprintf("%s.json cleanup: %s", c.base, renameErr.Error()))
				} else {
					changed = append(changed, fmt.Sprintf("%s/%s.json → %s.json.bak", paths.DataDir, c.base, c.base))
				}
			}
		}
	}

	out := SetupInitOut{
		OK:      len(errs) == 0,
		Root:    contentRoot,
		Created: created,
		Changed: changed,
		Next:    "config.toml and local.toml created — instruct the user to edit them by hand, then run the validate tool.",
	}
	if len(errs) > 0 {
		out.Errors = errs
	}
	return out, nil
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

// setupWritePlanTemplate copies the shipped plan-template-default.md
// byte-for-byte to <root>/.sdlc-v2/plan-template.md, so
// setup-plan-template.md's Step 2 can call this tool instead of a bare `cp`
// to a .sdlc-v2/ path. Byte-for-byte: os.ReadFile/os.WriteFile only, no
// parsing or re-serialization, matching the skill's own constraint that the
// shipped default be reproduced exactly.
func setupWritePlanTemplate(root string) (SetupInitOut, error) {
	defaultPath := resolveSkillTemplate("plan-template-default.md")
	if defaultPath == nil {
		return SetupInitOut{}, &mcpserver.DataError{
			Msg:        "setup_init: shipped plan-template-default.md not found — plugin installation may be corrupt",
			Suggestion: "Update or reinstall the sdlc plugin so plan-template-default.md ships with it. Then call setup_init again with writePlanTemplate true.",
		}
	}

	content, err := os.ReadFile(*defaultPath)
	if err != nil {
		return SetupInitOut{}, &mcpserver.InfraError{
			Msg:        fmt.Sprintf("read %s: %s", *defaultPath, err.Error()),
			Suggestion: "Check filesystem permissions on the plugin installation directory, then retry.",
			Cause:      err,
		}
	}

	sdlcDir := filepath.Join(root, paths.DataDir)
	if err := os.MkdirAll(sdlcDir, 0o755); err != nil {
		return SetupInitOut{}, &mcpserver.InfraError{
			Msg:        fmt.Sprintf("create %s directory: %s", paths.DataDir, err.Error()),
			Suggestion: "Create " + paths.DataDir + " manually with write permission, or free disk space on the project root, then retry setup_init with writePlanTemplate true.",
			Cause:      err,
		}
	}

	outPath := filepath.Join(sdlcDir, paths.PlanTemplateFile)
	if err := os.WriteFile(outPath, content, 0o644); err != nil {
		return SetupInitOut{}, &mcpserver.InfraError{
			Msg:        fmt.Sprintf("write %s: %s", outPath, err.Error()),
			Suggestion: "Check write permission on " + paths.DataDir + "/plan-template.md and free disk space on the project root, then retry setup_init with the same content.",
			Cause:      err,
		}
	}

	return SetupInitOut{
		OK:      true,
		Created: []string{paths.DataDir + "/plan-template.md"},
		Changed: []string{},
		Next:    "Plan template written to .sdlc-v2/plan-template.md — now the active template for plan's Step 2 planner.",
	}, nil
}

// setupWritePRTemplate persists in.Content verbatim to
// <root>/.sdlc-v2/pr-template.md, so setup-pr-template.md's Step 6 can call
// this tool instead of a bare Write to a .sdlc-v2/ path.
func setupWritePRTemplate(root string, in SetupInitIn) (SetupInitOut, error) {
	if in.Content == "" {
		return SetupInitOut{}, &mcpserver.DomainError{
			Msg:        "setup_init: content is required when writePRTemplate is true",
			Suggestion: "Pass the accepted PR template Markdown as content when writePRTemplate is true.",
		}
	}

	sdlcDir := filepath.Join(root, paths.DataDir)
	if err := os.MkdirAll(sdlcDir, 0o755); err != nil {
		return SetupInitOut{}, &mcpserver.InfraError{
			Msg:        fmt.Sprintf("create %s directory: %s", paths.DataDir, err.Error()),
			Suggestion: "Create " + paths.DataDir + " manually with write permission, or free disk space on the project root, then retry setup_init with writePRTemplate true.",
			Cause:      err,
		}
	}

	outPath := filepath.Join(sdlcDir, paths.PRTemplateFile)
	if err := os.WriteFile(outPath, []byte(in.Content), 0o644); err != nil {
		return SetupInitOut{}, &mcpserver.InfraError{
			Msg:        fmt.Sprintf("write %s: %s", outPath, err.Error()),
			Suggestion: "Check write permission on " + paths.DataDir + "/pr-template.md and free disk space on the project root, then retry setup_init with the same content.",
			Cause:      err,
		}
	}

	return SetupInitOut{
		OK:      true,
		Created: []string{paths.DataDir + "/pr-template.md"},
		Changed: []string{},
		Next:    "PR template written to .sdlc-v2/pr-template.md.",
	}, nil
}

// setupCheckTemplateExists reports whether <root>/.sdlc-v2/<name> exists,
// without reading its content, so setup/SKILL.md's snapshot steps can check
// for plan-template.md/pr-template.md through this tool instead of a bare
// Glob on a .sdlc-v2/ path.
func setupCheckTemplateExists(root, name string) (SetupInitOut, error) {
	path := filepath.Join(root, paths.DataDir, name)
	_, err := os.Stat(path)
	exists := err == nil
	if err != nil && !os.IsNotExist(err) {
		return SetupInitOut{}, &mcpserver.InfraError{
			Msg:        fmt.Sprintf("stat %s: %s", path, err.Error()),
			Suggestion: "Check filesystem permissions on the project root, then retry.",
			Cause:      err,
		}
	}

	next := fmt.Sprintf("%s/%s does not exist.", paths.DataDir, name)
	if exists {
		next = fmt.Sprintf("%s/%s exists.", paths.DataDir, name)
	}

	return SetupInitOut{
		OK:      true,
		Created: []string{},
		Changed: []string{},
		Exists:  exists,
		Next:    next,
	}, nil
}

// setupReadPlanTemplate reports whether <root>/.sdlc-v2/plan-template.md
// exists and, when it does, its full content, so setup-plan-template.md's
// Steps 1 and 3 can show the file's content through this tool instead of a
// bare Read on a .sdlc-v2/ path.
func setupReadPlanTemplate(root string) (SetupInitOut, error) {
	path := filepath.Join(root, paths.DataDir, paths.PlanTemplateFile)
	content, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return SetupInitOut{
				OK:      true,
				Created: []string{},
				Changed: []string{},
				Exists:  false,
				Content: "",
				Next:    paths.DataDir + "/plan-template.md does not exist — nothing to show.",
			}, nil
		}
		return SetupInitOut{}, &mcpserver.InfraError{
			Msg:        fmt.Sprintf("read %s: %s", path, err.Error()),
			Suggestion: "Check filesystem permissions on the project root, then retry.",
			Cause:      err,
		}
	}

	return SetupInitOut{
		OK:      true,
		Created: []string{},
		Changed: []string{},
		Exists:  true,
		Content: string(content),
		Next:    "Show content to the user before deciding whether to overwrite, or summarize its sections.",
	}, nil
}

// ---------------------------------------------------------------------------
// Registration
// ---------------------------------------------------------------------------

// RegisterSetupTools registers setup_prepare and setup_init on the server.
func RegisterSetupTools(s *mcpserver.Server) {
	mcpserver.Register(s, "setup_prepare",
		"Returns the canonical section descriptors for setup, with per-section field metadata and runtime-detected defaults (defaultBranch, remoteOwner). Optionally checks config migration state. Also reports ciScriptDrift: per-script version comparison against the embedded scaffold_ci payloads, flagging outdated or not-yet-installed CI scripts (remediate with scaffold_ci({force:true})). Optional: explain (\"<sectionId>.<fieldName>\", for example \"ship.rebase\") returns only the explanation of that option (details, examples, type, options, default, config file and path) with a next step: next tells the skill to show the explanation and ask the open question again. A value without a dot, an unknown section, a section with no fields, or an unknown field returns a DomainError with a Suggestion.",
		mcpserver.Annotations{
			Title:      "Prepare SDLC setup context",
			ReadOnly:   true,
			Idempotent: true,
			OpenWorld:  false,
		},
		func(ctx mcpserver.Ctx, in SetupPrepareIn) (any, error) {
			root, err := worktree.MainRoot()
			if err != nil {
				// Fallback to cwd — setup must work before any context exists.
				root, err = os.Getwd()
				if err != nil {
					return nil, &mcpserver.InfraError{
						Msg:        fmt.Sprintf("resolve project root: %s", err.Error()),
						Suggestion: "Restart the sdlc MCP server from a directory that still exists, then retry setup_prepare.",
						Cause:      err,
					}
				}
			}
			driftRoot := root
			if active, err := worktree.ActiveRoot(); err == nil {
				driftRoot = active
			}
			out, err := setupPrepareWithDrift(root, driftRoot, in)
			if err != nil {
				return nil, err
			}
			// setupPrepareResult drops NeedsMigration/Sections/CIScriptDrift
			// for explain mode's reply -- see its doc comment and
			// setupPrepareExplainOut's.
			return setupPrepareResult(out), nil
		},
	)

	mcpserver.Register(s, "setup_init",
		"Creates the .sdlc-v2/ directory scaffold for a v1 (TOML) config: .sdlc-v2/.gitignore, root .gitignore managed block, config.toml, and local.toml. Writes the complete, heavily-commented templates directly to disk (never through LLM context) — every field is present, with inline docs and example guardrails. Idempotent: an existing config.toml/local.toml is left untouched. Also renames stale config.json/local.json to .bak (skipped if .bak already exists). Instruct the user to edit the files by hand, then run the validate tool. With writePlanTemplate:true, instead copies the shipped plan-template-default.md byte-for-byte to .sdlc-v2/plan-template.md. With writePRTemplate:true, instead writes content verbatim to .sdlc-v2/pr-template.md. With checkPlanTemplate:true or checkPRTemplate:true, instead reports whether plan-template.md/pr-template.md exists (out.exists), without reading it. With readPlanTemplate:true, instead reports whether plan-template.md exists and its full content (out.exists, out.content). Git-tracked files (config.toml, both .gitignore blocks, plan-template.md, pr-template.md) go under the active git worktree, returned as root; local.toml and runs/ go under the main worktree.",
		mcpserver.Annotations{
			Title:       "Initialize SDLC config files",
			ReadOnly:    false,
			Destructive: true,
			Idempotent:  true,
			OpenWorld:   false,
		},
		func(ctx mcpserver.Ctx, in SetupInitIn) (SetupInitOut, error) {
			stateRoot, err := worktree.MainRoot()
			if err != nil {
				stateRoot, err = os.Getwd()
				if err != nil {
					return SetupInitOut{}, &mcpserver.InfraError{
						Msg:        fmt.Sprintf("resolve project root: %s", err.Error()),
						Suggestion: "Restart the sdlc MCP server from a directory that still exists, then retry setup_init.",
						Cause:      err,
					}
				}
			}
			contentRoot := stateRoot
			if active, err := worktree.ActiveRoot(); err == nil {
				contentRoot = active
			}
			return setupInitRoots(contentRoot, stateRoot, in)
		},
	)
}
