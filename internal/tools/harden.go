package tools

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/configmigrate"
	"github.com/rnagrodzki/sdlc-plugin/internal/dimensions"
	"github.com/rnagrodzki/sdlc-plugin/internal/execx"
	"github.com/rnagrodzki/sdlc-plugin/internal/frontmatter"
	"github.com/rnagrodzki/sdlc-plugin/internal/fsx"
	"github.com/rnagrodzki/sdlc-plugin/internal/gitx"
	"github.com/rnagrodzki/sdlc-plugin/internal/history"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
	"github.com/rnagrodzki/sdlc-plugin/internal/state"
	"github.com/rnagrodzki/sdlc-plugin/internal/worktree"
)

// hardenPluginRepoURL is harden-prepare.js's PLUGIN_REPO_URL literal.
// Deliberately NOT shared with error_report.go's errorReportTargetRepo
// constant — source keeps the two independent so each script has a single,
// locally-visible source of truth (source comment, confirmed in fact
// sheet); the Go port preserves that separation.
const hardenPluginRepoURL = "https://github.com/rnagrodzki/sdlc-plugin"

// hardenIssueNumberRe validates --from-issue as a bare positive integer,
// mirroring source's defense-in-depth regex check (the argv-array gh call
// below already avoids shell metacharacter parsing on its own).
var hardenIssueNumberRe = regexp.MustCompile(`^\d+$`)

// ---------------------------------------------------------------------------
// Input / Output
// ---------------------------------------------------------------------------

// HardenPrepareIn is harden_prepare's input. FailureText/Skill are required
// (checked after --from-issue processing, since an issue body can supply
// FailureText); all other string fields are optional with empty-string
// defaults, matching source's `!= null` checks.
type HardenPrepareIn struct {
	FailureText string `json:"failureText"`
	Skill       string `json:"skill"`

	Step       string `json:"step,omitempty"`
	Operation  string `json:"operation,omitempty"`
	ExitCode   string `json:"exitCode,omitempty"`
	ErrorType  string `json:"errorType,omitempty"`
	UserIntent string `json:"userIntent,omitempty"`
	ArgsString string `json:"argsString,omitempty"`

	// FromIssue, when set, is mutually exclusive with FailureText (R19):
	// the manifest's failure.text is populated from the issue body instead.
	FromIssue string `json:"fromIssue,omitempty"`

	// SkipConfigCheck gates the KD5 configmigrate.Verify call. Source:
	// ensureConfigVersion(cwdForVerify, { skip: skipConfigCheck, ... }).
	SkipConfigCheck bool `json:"skipConfigCheck,omitempty"`
}

// HardenPrepareOut is harden_prepare's output: the path to the written
// manifest (KD4 file handoff).
type HardenPrepareOut struct {
	ManifestPath string `json:"manifestPath"`
}

// ---------------------------------------------------------------------------
// Manifest shape (source: harden-prepare.js lines 286-318)
// ---------------------------------------------------------------------------

type hardenFailure struct {
	Text       string  `json:"text"`
	Skill      string  `json:"skill"`
	Step       string  `json:"step"`
	Operation  string  `json:"operation"`
	ExitCode   *string `json:"exitCode"`
	ErrorType  string  `json:"errorType"`
	UserIntent string  `json:"userIntent"`
	ArgsString string  `json:"argsString"`
}

type reviewDimensionMeta struct {
	Name        string   `json:"name"`
	Severity    string   `json:"severity"`
	Description string   `json:"description"`
	Triggers    []string `json:"triggers"`
	Model       string   `json:"model"`
	Path        string   `json:"path"`
}

type copilotInstruction struct {
	ApplyTo string `json:"applyTo"`
	Name    string `json:"name"`
	Path    string `json:"path"`
}

type hardenSurfaces struct {
	PlanGuardrails       []surfaceGuardrail    `json:"planGuardrails"`
	ExecuteGuardrails    []surfaceGuardrail    `json:"executeGuardrails"`
	ReviewDimensions     []reviewDimensionMeta `json:"reviewDimensions"`
	CopilotInstructions  []copilotInstruction  `json:"copilotInstructions"`
	ErrorReportSkillPath string                `json:"errorReportSkillPath"`
}

// hardenShipState / hardenExecuteState mirror readPipelineState()'s field
// selection exactly (source lines 100-131). CurrentStep is `any` because
// source does `data.X || null`, which passes the original JSON value
// through unchanged (string, number, or object) rather than coercing to a
// fixed type — only falsy values collapse to null. LastFailedStep/
// FailedTask/FailedWave are typed fields (Task 19): each has exactly one
// dedicated writer (ship_state.go / execute_state.go), so their Go type is
// known and asserted directly instead of passed through as `any`.
type hardenShipState struct {
	Paused         bool   `json:"paused"`
	CurrentStep    any    `json:"currentStep"`
	LastFailedStep string `json:"lastFailedStep,omitempty"`
}

type hardenExecuteState struct {
	FailedTask string `json:"failedTask,omitempty"`
	FailedWave int    `json:"failedWave,omitempty"`
}

// hardenPipeline.Issues merges the issues[] accumulator (StateIssue, defined
// in execute_state.go) from both the ship-state and execute-state files
// found by readHardenPipelineState, ship first then execute, in the order
// each was appended on disk. Nil/omitted when neither state carries any
// issues, so older state files without issues[] round-trip unchanged
// (backward compatible — Task 19 AC).
type hardenPipeline struct {
	ShipState    *hardenShipState    `json:"shipState"`
	ExecuteState *hardenExecuteState `json:"executeState"`
	Issues       []StateIssue        `json:"issues,omitempty"`
}

type hardenRepository struct {
	Root              string `json:"root"`
	ContentRoot       string `json:"contentRoot"`
	Branch            string `json:"branch"`
	RecentDiffSummary string `json:"recentDiffSummary"`
}

// hardenHistory carries recent run records and open deferred issues from the
// persistent history store (.sdlc-v2/history/), providing the orchestrator
// with evidence of recurring patterns (e.g. same guardrail hit across 3+
// runs triggers severity escalation in proposals).
type hardenHistory struct {
	RecentRuns   []history.RunRecord     `json:"recentRuns,omitempty"`
	OpenDeferred []history.DeferredIssue `json:"openDeferred,omitempty"`
}

type hardenManifest struct {
	Failure hardenFailure `json:"failure"`
	// classification_hint is deliberately snake_case (matches source
	// byte-for-byte), unlike every other camelCase manifest key.
	ClassificationHint *string            `json:"classification_hint"`
	Surfaces           hardenSurfaces     `json:"surfaces"`
	Pipeline           hardenPipeline     `json:"pipeline"`
	Repository         hardenRepository   `json:"repository"`
	History            *hardenHistory     `json:"history,omitempty"`
	PluginRepoURL      string             `json:"pluginRepoUrl"`
	Timestamp          string             `json:"timestamp"`
	Errors             []surfaceLoadError `json:"errors"`
}

// ---------------------------------------------------------------------------
// Loaders 2-4 of 4 (loadGuardrails/surfaceGuardrail lives in guardrails.go)
// ---------------------------------------------------------------------------

// loadReviewDimensions is the Go port of harden-surfaces.js's
// loadReviewDimensions(projectRoot, errors). Per RULING 1 (orchestrator),
// this surface stays metadata-only — name/severity/description/triggers/
// model/path extracted from frontmatter — with no rendered instruction
// content and no dimensions.ToInstructions call, mirroring source exactly.
//
// dimensions.Load already returns (nil, nil) when the directory is
// missing, matching source's `if (!fs.existsSync(dir)) return [];` with no
// error pushed. A directory-read failure is reported once under the
// "review-dimensions" surface; a per-file frontmatter/read failure is
// reported per file and that file is skipped (source: try/catch per
// iteration, loop continues).
func loadReviewDimensions(contentRoot string, errs *[]surfaceLoadError) []reviewDimensionMeta {
	dir := filepath.Join(contentRoot, paths.DataDir, "review-dimensions")
	dims, err := dimensions.Load(dir)
	if err != nil {
		*errs = append(*errs, surfaceLoadError{
			Surface: "review-dimensions",
			Message: fmt.Sprintf("readdir failed: %s", err.Error()),
		})
		return []reviewDimensionMeta{}
	}

	out := make([]reviewDimensionMeta, 0, len(dims))
	for _, d := range dims {
		if d.Err != nil {
			if errors.Is(d.Err, frontmatter.ErrNoFrontmatter) {
				*errs = append(*errs, surfaceLoadError{
					Surface: "review-dimensions",
					Message: fmt.Sprintf("Missing frontmatter: %s", d.File),
				})
			} else {
				*errs = append(*errs, surfaceLoadError{
					Surface: "review-dimensions",
					Message: fmt.Sprintf("Read/parse failed for %s: %s", d.File, d.Err.Error()),
				})
			}
			continue
		}
		out = append(out, reviewDimensionMeta{
			Name:        stringOrDefault(d.Meta["name"], ""),
			Severity:    stringOrDefault(d.Meta["severity"], ""),
			Description: stringOrDefault(d.Meta["description"], ""),
			Triggers:    anySliceToStrings(d.Meta["triggers"]),
			Model:       stringOrDefault(d.Meta["model"], ""),
			Path:        filepath.Join(dir, d.File),
		})
	}
	return out
}

// loadCopilotInstructions is the Go port of harden-surfaces.js's
// loadCopilotInstructions(projectRoot, errors): reads every
// *.instructions.md file under .github/instructions and extracts only
// {applyTo, name, path}. Unlike loadReviewDimensions, a missing frontmatter
// block here is NOT an error — source defaults to an empty frontmatter map
// (`fmRaw ? parseSimpleYaml(fmRaw) : {}`) and still emits the entry.
func loadCopilotInstructions(contentRoot string, errs *[]surfaceLoadError) []copilotInstruction {
	dir := filepath.Join(contentRoot, ".github", "instructions")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []copilotInstruction{}
		}
		*errs = append(*errs, surfaceLoadError{
			Surface: "copilot-instructions",
			Message: fmt.Sprintf("readdir failed: %s", err.Error()),
		})
		return []copilotInstruction{}
	}

	out := make([]copilotInstruction, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".instructions.md") {
			continue
		}
		filePath := filepath.Join(dir, name)
		content, readErr := os.ReadFile(filePath)
		if readErr != nil {
			*errs = append(*errs, surfaceLoadError{
				Surface: "copilot-instructions",
				Message: fmt.Sprintf("Read failed for %s: %s", name, readErr.Error()),
			})
			continue
		}
		meta, _, fmErr := frontmatter.Parse(content)
		if fmErr != nil {
			if !errors.Is(fmErr, frontmatter.ErrNoFrontmatter) {
				*errs = append(*errs, surfaceLoadError{
					Surface: "copilot-instructions",
					Message: fmt.Sprintf("Read failed for %s: %s", name, fmErr.Error()),
				})
				continue
			}
			meta = map[string]any{}
		}
		out = append(out, copilotInstruction{
			ApplyTo: stringOrDefault(meta["applyTo"], ""),
			Name:    strings.TrimSuffix(name, ".instructions.md"),
			Path:    filePath,
		})
	}
	return out
}

// findPluginRootFrom walks up from start looking for a directory
// containing .claude-plugin/plugin.json. It is a deliberately simplified,
// single-strategy stand-in for internal/hooks's unexported
// resolvePluginRoot (which has a fuller 3-tier exe-dir/cwd/
// scan-~/.claude/plugins strategy, per Task 37) — out of this task's Files
// scope to export or duplicate in full. resolveErrorReportSkill's use case
// is low-stakes and soft-fail-only (a single manifest field, non-fatal on
// failure), so this simpler version is a proportionate substitute; flagged
// as a known simplification relative to session_start.go's precedent.
func findPluginRootFrom(start string) (string, bool) {
	dir := start
	for {
		if _, err := os.Stat(filepath.Join(dir, ".claude-plugin", "plugin.json")); err == nil {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

// resolveErrorReportSkill is the Go port of harden-surfaces.js's
// resolveErrorReportSkill(projectRoot, errors). Source's own implementation
// ignores its projectRoot parameter entirely — it resolves the sibling
// skills/error-report/REFERENCE.md path via __dirname (the plugin's
// own lib/ directory), not via the caller-supplied project root. There is
// no __dirname in a compiled Go binary, so this walks up from the running
// executable's directory (falling back to the working directory) looking
// for the plugin's own .claude-plugin/plugin.json, mirroring source's
// "resolve sibling of the plugin's own installation" intent as closely as
// a compiled binary allows.
func resolveErrorReportSkill(errs *[]surfaceLoadError) string {
	const relPath = "skills/error-report/REFERENCE.md"

	start := ""
	if exe, err := os.Executable(); err == nil {
		start = filepath.Dir(exe)
	} else if cwd, cerr := os.Getwd(); cerr == nil {
		start = cwd
	}
	if start == "" {
		*errs = append(*errs, surfaceLoadError{
			Surface: "error-report-skill",
			Message: fmt.Sprintf("%s not found (unable to resolve plugin root)", relPath),
		})
		return ""
	}

	root, ok := findPluginRootFrom(start)
	if !ok {
		*errs = append(*errs, surfaceLoadError{
			Surface: "error-report-skill",
			Message: fmt.Sprintf("%s not found (plugin root not located from %s)", relPath, start),
		})
		return ""
	}

	resolved := filepath.Join(root, "skills", "error-report", "REFERENCE.md")
	if _, err := os.Stat(resolved); err != nil {
		*errs = append(*errs, surfaceLoadError{
			Surface: "error-report-skill",
			Message: fmt.Sprintf("%s not found at %s", relPath, resolved),
		})
		return ""
	}
	if abs, err := filepath.Abs(resolved); err == nil {
		return abs
	}
	return resolved
}

// anySliceToStrings extracts the string elements of a decoded-JSON `[]any`
// value, matching source's `Array.isArray(fm.triggers) ? fm.triggers : []`
// for the common case (a string array); a non-array value or non-string
// element yields an empty result / is dropped rather than erroring, since
// this is best-effort metadata, not a validated field.
func anySliceToStrings(v any) []string {
	raw, ok := v.([]any)
	if !ok {
		return []string{}
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// R16 dimension pre-flight (harden-prepare.js lines 253-268, dimensions half;
// guardrails half lives in guardrails.go's guardrailsPreflight)
// ---------------------------------------------------------------------------

// dimensionsPreflight validates every review-dimension file under
// <contentRoot>/.sdlc-v2/review-dimensions using dimensions.Load/Validate
// (Task 11), formatting each finding as "existing-review-dimension
// <file>: <msg>" to match source's preflightErrors.push(...) format
// exactly. dimensions.Load's own (nil, nil)-on-missing-directory
// convention already mirrors source's `if (fs.existsSync(dimDir))` guard,
// so a missing directory yields no errors here, matching source.
func dimensionsPreflight(contentRoot string) []string {
	dir := filepath.Join(contentRoot, paths.DataDir, "review-dimensions")
	dims, err := dimensions.Load(dir)
	if err != nil {
		return []string{fmt.Sprintf("review-dimensions: readdir failed: %s", err.Error())}
	}
	var errs []string
	for _, d := range dims {
		for _, msg := range dimensions.Validate(d) {
			errs = append(errs, fmt.Sprintf("existing-review-dimension %s: %s", d.File, msg))
		}
	}
	return errs
}

// ---------------------------------------------------------------------------
// Pipeline state (harden-prepare.js lines 96-131; branch-agnostic per
// state.FindAny, see Open Question 4 / RULING 4)
// ---------------------------------------------------------------------------

// orNil mirrors JS's `x || null`: a JS-falsy decoded value (nil, false, 0,
// "", empty map/slice — see jsFalsyLocal in validators.go) collapses to
// nil; anything else passes through unchanged, preserving its original
// JSON type rather than coercing it.
func orNil(v any) any {
	if jsFalsyLocal(v) {
		return nil
	}
	return v
}

// stringField type-asserts data[key] to string, returning "" for anything
// else (missing key, nil, or a non-string JSON value).
func stringField(data map[string]any, key string) string {
	s, _ := data[key].(string)
	return s
}

// intField type-asserts data[key] to int. JSON numbers always decode to
// float64 (state.FindAny re-parses from disk on every call), so that is the
// only numeric case handled; anything else returns 0.
func intField(data map[string]any, key string) int {
	if f, ok := data[key].(float64); ok {
		return int(f)
	}
	return 0
}

// extractStateIssues type-asserts data["issues"] into []StateIssue via a
// JSON marshal/unmarshal round-trip — the inverse of execAppendIssue's own
// round-trip, and the same idiom since state.FindAny always hands back
// issues[] entries as []any of map[string]any. Missing key, wrong shape, or
// any marshal/unmarshal error all yield nil (no issues), never an error:
// mirrors readHardenPipelineState's existing fail-open behavior for state.
func extractStateIssues(data map[string]any) []StateIssue {
	raw, ok := data["issues"]
	if !ok || raw == nil {
		return nil
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	var issues []StateIssue
	if err := json.Unmarshal(b, &issues); err != nil {
		return nil
	}
	return issues
}

// readHardenPipelineState is the Go port of harden-prepare.js's
// readPipelineState(): looks up the most recent ship/execute state file
// project-wide (state.FindAny — no branch filter, matching source's
// detectResumeState({prefix}) call with no branch argument) and extracts
// only the fields the manifest needs. A missing file or a read/parse
// failure both leave the corresponding state nil — source only logs the
// latter to stderr, it does not surface it as a manifest or surface-load
// error, so neither case is recorded in errs here. The issues[] accumulator
// (Task 19) is merged from both state files, ship first then execute.
func readHardenPipelineState(root string) (*hardenShipState, *hardenExecuteState, []StateIssue) {
	var issues []StateIssue

	var shipState *hardenShipState
	if st, err := state.FindAny(root, "ship"); err == nil && st != nil {
		shipState = &hardenShipState{
			Paused:         !jsFalsyLocal(st.Data["paused"]),
			CurrentStep:    orNil(st.Data["currentStep"]),
			LastFailedStep: stringField(st.Data, "lastFailedStep"),
		}
		issues = append(issues, extractStateIssues(st.Data)...)
	}

	var executeState *hardenExecuteState
	if st, err := state.FindAny(root, "execute"); err == nil && st != nil {
		executeState = &hardenExecuteState{
			FailedTask: stringField(st.Data, "failedTask"),
			FailedWave: intField(st.Data, "failedWave"),
		}
		issues = append(issues, extractStateIssues(st.Data)...)
	}

	return shipState, executeState, issues
}

// ---------------------------------------------------------------------------
// activeWorktreeRootSafe: thin fail-open wrapper around worktree.ActiveRoot
// ---------------------------------------------------------------------------

// activeWorktreeRootSafe mirrors source's resolveActiveWorktreeSafe(): try
// the active worktree root, fall back to the working directory, fall back
// to "" — never erroring. internal/worktree.ActiveRoot itself deliberately
// does NOT fail open (package doc comment, confirmed), and internal/tools
// has no shared "Safe" helper (that pattern only exists, unexported, in
// internal/hooks/session_start.go), so this is a small local copy of the
// same fail-open shape, scoped to this package.
func activeWorktreeRootSafe() string {
	if root, err := worktree.ActiveRoot(); err == nil {
		return root
	}
	if cwd, err := os.Getwd(); err == nil {
		return cwd
	}
	return ""
}

// ---------------------------------------------------------------------------
// gh issue view (R19 --from-issue)
// ---------------------------------------------------------------------------

type ghIssueViewResult struct {
	Body   string `json:"body"`
	Labels []any  `json:"labels"`
	Title  string `json:"title"`
}

// issueLabelNames extracts label names from a gh issue view --json labels
// result, matching source's `labels.map(l => typeof l === 'string' ? l :
// l.name)` — an element is either a bare label-name string or an
// {name: string, ...} object, depending on the gh version/output shape.
func issueLabelNames(raw []any) []string {
	names := make([]string, 0, len(raw))
	for _, l := range raw {
		switch v := l.(type) {
		case string:
			names = append(names, v)
		case map[string]any:
			if n, ok := v["name"].(string); ok {
				names = append(names, n)
			}
		}
	}
	return names
}

// ---------------------------------------------------------------------------
// hardenPrepare
// ---------------------------------------------------------------------------

// hardenPrepare is the Go port of harden-prepare.js's main(). root is the
// main worktree (config/guardrails, #360 R-projectroot); contentRoot is the
// active worktree (branch-tracked dimensions/copilot instructions, #474).
func hardenPrepare(root, contentRoot string, in HardenPrepareIn) (HardenPrepareOut, error) {
	// KD5 — param-first config-version gate.
	if !in.SkipConfigCheck {
		if err := configmigrate.Verify(root); err != nil {
			return HardenPrepareOut{}, &mcpserver.DataError{
				Msg:   fmt.Sprintf("config-version: %s", err.Error()),
				Cause: err,
			}
		}
	}

	hasFailureText := strings.TrimSpace(in.FailureText) != ""
	hasFromIssue := strings.TrimSpace(in.FromIssue) != ""

	// R19 — --from-issue mutual exclusion with --failure-text.
	if hasFailureText && hasFromIssue {
		return HardenPrepareOut{}, &mcpserver.DomainError{
			Msg: "--failure-text and --from-issue are mutually exclusive — provide one or the other, not both",
		}
	}

	failureText := in.FailureText
	var classificationHint *string

	// R19 — fetch issue body when --from-issue is used.
	if hasFromIssue {
		issueNum := strings.TrimSpace(in.FromIssue)
		if !hardenIssueNumberRe.MatchString(issueNum) {
			return HardenPrepareOut{}, &mcpserver.DomainError{
				Msg: fmt.Sprintf("--from-issue: invalid issue number %q — must be a positive integer", issueNum),
			}
		}

		out, err := execx.Run("gh", []string{"issue", "view", issueNum, "--json", "body,labels,title"}, execx.Options{Dir: root})
		if err != nil {
			return HardenPrepareOut{}, &mcpserver.InfraError{
				Msg:   fmt.Sprintf("--from-issue %s: gh issue view failed: %s", issueNum, err.Error()),
				Cause: err,
			}
		}

		var issueJSON ghIssueViewResult
		if jsonErr := json.Unmarshal([]byte(out), &issueJSON); jsonErr != nil {
			return HardenPrepareOut{}, &mcpserver.InfraError{
				Msg:   fmt.Sprintf("--from-issue %s: gh issue view returned invalid JSON: %s", issueNum, jsonErr.Error()),
				Cause: jsonErr,
			}
		}

		failureText = issueJSON.Body
		if containsStr(issueLabelNames(issueJSON.Labels), "mcp-failure") {
			hint := "plugin-defect"
			classificationHint = &hint
		}
	}

	// Required-field check happens AFTER --from-issue processing, so an
	// empty issue body can still trigger "Missing required field: failureText".
	var missing []string
	if strings.TrimSpace(failureText) == "" {
		missing = append(missing, "failureText")
	}
	if strings.TrimSpace(in.Skill) == "" {
		missing = append(missing, "skill")
	}
	if len(missing) > 0 {
		msgs := make([]string, len(missing))
		for i, m := range missing {
			msgs[i] = "Missing required field: " + m
		}
		return HardenPrepareOut{}, &mcpserver.DomainError{Msg: strings.Join(msgs, "; ")}
	}

	// R16 — pre-flight validation. Any error aborts before the manifest is
	// ever assembled; no manifest file is written.
	var preflightErrors []string
	preflightErrors = append(preflightErrors, guardrailsPreflight(root)...)
	preflightErrors = append(preflightErrors, dimensionsPreflight(contentRoot)...)
	if len(preflightErrors) > 0 {
		return HardenPrepareOut{}, &mcpserver.DomainError{
			Msg: fmt.Sprintf("pre-flight validation failed: %s", strings.Join(preflightErrors, "; ")),
		}
	}

	// Load all five surfaces deterministically (R4).
	loadErrs := []surfaceLoadError{}
	planGuardrails := loadSurfaceGuardrails(root, "plan", &loadErrs)
	executeGuardrails := loadSurfaceGuardrails(root, "execute", &loadErrs)
	reviewDimensions := loadReviewDimensions(contentRoot, &loadErrs)
	copilotInstructions := loadCopilotInstructions(contentRoot, &loadErrs)
	errorReportSkillPath := resolveErrorReportSkill(&loadErrs)

	shipState, executeState, pipelineIssues := readHardenPipelineState(root)

	branch, _ := gitx.CurrentBranch(contentRoot)
	recentDiffSummary, _ := execx.Run("git", []string{"diff", "--shortstat", "HEAD~1..HEAD"}, execx.Options{Dir: contentRoot})

	var exitCode *string
	if in.ExitCode != "" {
		v := in.ExitCode
		exitCode = &v
	}

	manifest := hardenManifest{
		Failure: hardenFailure{
			Text:       failureText,
			Skill:      strings.TrimSpace(in.Skill),
			Step:       strings.TrimSpace(in.Step),
			Operation:  strings.TrimSpace(in.Operation),
			ExitCode:   exitCode,
			ErrorType:  strings.TrimSpace(in.ErrorType),
			UserIntent: in.UserIntent,
			ArgsString: in.ArgsString,
		},
		ClassificationHint: classificationHint,
		Surfaces: hardenSurfaces{
			PlanGuardrails:       planGuardrails,
			ExecuteGuardrails:    executeGuardrails,
			ReviewDimensions:     reviewDimensions,
			CopilotInstructions:  copilotInstructions,
			ErrorReportSkillPath: errorReportSkillPath,
		},
		Pipeline: hardenPipeline{
			ShipState:    shipState,
			ExecuteState: executeState,
			Issues:       pipelineIssues,
		},
		Repository: hardenRepository{
			Root:              root,
			ContentRoot:       contentRoot,
			Branch:            branch,
			RecentDiffSummary: recentDiffSummary,
		},
		PluginRepoURL: hardenPluginRepoURL,
		Timestamp:     time.Now().UTC().Format(time.RFC3339),
		Errors:        loadErrs,
	}

	tmpDir, err := os.MkdirTemp("", "sdlc-harden-")
	if err != nil {
		return HardenPrepareOut{}, &mcpserver.InfraError{Msg: fmt.Sprintf("create temp dir: %s", err.Error()), Cause: err}
	}
	manifestPath := filepath.Join(tmpDir, "manifest.json")
	if err := fsx.AtomicWriteJSON(manifestPath, manifest); err != nil {
		return HardenPrepareOut{}, &mcpserver.InfraError{Msg: fmt.Sprintf("write manifest: %s", err.Error()), Cause: err}
	}

	return HardenPrepareOut{ManifestPath: manifestPath}, nil
}
