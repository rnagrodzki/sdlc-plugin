package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/rnagrodzki/sdlc-plugin/internal/history"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
	"github.com/rnagrodzki/sdlc-plugin/internal/worktree"
)

// ---------------------------------------------------------------------------
// prepare_orchestrator: merges harden_prepare and error_report_prepare
// behind a single Mode discriminator. Both source tools already followed
// the same shape (resolve worktree.MainRoot(), delegate to a pure core
// function, write a manifest to a temp file, return its path — the KD4
// pattern); this tool keeps hardenPrepare/errorReportPrepare exactly as
// they are (harden.go / error_report.go) and only unifies the MCP-facing
// registration.
// ---------------------------------------------------------------------------

// PrepareOrchestratorIn is the union of HardenPrepareIn and
// ErrorReportPrepareIn, discriminated by Mode ("harden" | "error_report").
//
// Field-by-field origin:
//   - Mode is new: selects which branch runs.
//   - Skill/Step/Operation/ErrorType/UserIntent/ArgsString are shared by
//     name across both source structs (identical JSON tags in both).
//   - FailureText/FromIssue/SkipConfigCheck are harden-only (HardenPrepareIn)
//     and are ignored when Mode=="error_report".
//   - Error (wire tag errorText)/ExitOrHTTPCode/SuggestedInvestigation are
//     error_report-only (ErrorReportPrepareIn) and are ignored when
//     Mode=="harden".
//   - ExitCode (harden) and ExitOrHTTPCode (error_report) are deliberately
//     kept as two separate fields with two separate tags — they are NOT
//     the same field under two names, despite both meaning "exit/HTTP
//     code": harden mode reads ExitCode, error_report mode reads
//     ExitOrHTTPCode, and the other is simply unused for that mode.
//
// FailureText and Error(errorText) are each marked omitempty here even
// though their respective source structs did not tag them that way: in the
// source structs each was the sole required "content" field of a
// single-purpose tool, but in the merged schema neither can be
// unconditionally required without wrongly forcing the *other* mode's
// caller to also supply it. Requiredness is still enforced at runtime,
// per mode, by hardenPrepare/errorReportPrepare themselves — unchanged.
//
// Step and Operation carry a genuine tag conflict between the two source
// structs: HardenPrepareIn tags them omitempty (optional — harden mode
// never required them), ErrorReportPrepareIn does not (required — checked
// by errorReportPrepare). Since this is one merged field per name, one tag
// wins; omitempty was chosen so the schema doesn't overconstrain harden
// mode. As with FailureText/Error above, error_report mode's actual
// requiredness check is unaffected: errorReportPrepare still rejects a
// missing step/operation at runtime.
type PrepareOrchestratorIn struct {
	// Mode selects which prepare pipeline runs: "harden" or "error_report".
	Mode string `json:"mode" jsonschema_description:"Which prepare pipeline runs: \"harden\" or \"error_report\"."`

	// --- shared across both modes ---
	Skill      string `json:"skill" jsonschema_description:"Name of the skill that was executing when the failure occurred."`
	Step       string `json:"step,omitempty" jsonschema_description:"Pipeline step that was executing when the failure occurred, if applicable."`
	Operation  string `json:"operation,omitempty" jsonschema_description:"Operation being attempted when the failure occurred."`
	ErrorType  string `json:"errorType,omitempty" jsonschema_description:"Classification of the error, if known."`
	UserIntent string `json:"userIntent,omitempty" jsonschema_description:"What the user was trying to accomplish."`
	ArgsString string `json:"argsString,omitempty" jsonschema_description:"Raw argument string the skill/command was invoked with."`

	// --- harden mode only (HardenPrepareIn) ---
	FailureText     string `json:"failureText,omitempty" jsonschema_description:"Harden mode only. Raw failure text/output to analyze."`
	ExitCode        string `json:"exitCode,omitempty" jsonschema_description:"Harden mode only. Process exit code observed at failure."`
	FromIssue       string `json:"fromIssue,omitempty" jsonschema_description:"Harden mode only. Source issue number/reference this hardening pass is derived from, if any."`
	SkipConfigCheck bool   `json:"skipConfigCheck,omitempty" jsonschema_description:"Harden mode only. Skips the config-version auto-migration gate normally run before preflight checks."`

	// --- error_report mode only (ErrorReportPrepareIn) ---
	Error                  string `json:"errorText,omitempty" jsonschema_description:"Error_report mode only. Raw error text to report."`
	ExitOrHTTPCode         string `json:"exitOrHttpCode,omitempty" jsonschema_description:"Error_report mode only. Process exit code or HTTP status observed at failure."`
	SuggestedInvestigation string `json:"suggestedInvestigation,omitempty" jsonschema_description:"Error_report mode only. Suggested next steps for investigating the failure, included in the drafted issue."`

	// --- harden mode optional: history context ---
	// HistoryPath, when set, points to .sdlc-v2/history/. The manifest
	// includes recent run records and open deferred issues as additional
	// evidence for hardening proposals.
	HistoryPath string `json:"historyPath,omitempty" jsonschema_description:"Harden mode only. Path to .sdlc-v2/history/. When set, the manifest includes recent run records and open deferred issues as additional evidence for hardening proposals."`
}

// PrepareOrchestratorOut is prepare_orchestrator's output: the path to the
// written manifest (KD4 file handoff, same shape both source tools already
// used), plus the Mode that was actually run.
type PrepareOrchestratorOut struct {
	ManifestPath string `json:"manifestPath"`
	Mode         string `json:"mode"`
}

// ---------------------------------------------------------------------------
// Field mapping (pure, unit-testable in isolation from worktree/filesystem
// state — see prepare_orchestrator_test.go's field-fidelity tests)
// ---------------------------------------------------------------------------

// toHardenPrepareIn maps the harden-relevant subset of PrepareOrchestratorIn
// onto HardenPrepareIn. Every HardenPrepareIn field must be assigned here.
func toHardenPrepareIn(in PrepareOrchestratorIn) HardenPrepareIn {
	return HardenPrepareIn{
		FailureText:     in.FailureText,
		Skill:           in.Skill,
		Step:            in.Step,
		Operation:       in.Operation,
		ExitCode:        in.ExitCode,
		ErrorType:       in.ErrorType,
		UserIntent:      in.UserIntent,
		ArgsString:      in.ArgsString,
		FromIssue:       in.FromIssue,
		SkipConfigCheck: in.SkipConfigCheck,
	}
}

// toErrorReportPrepareIn maps the error_report-relevant subset of
// PrepareOrchestratorIn onto ErrorReportPrepareIn. Every ErrorReportPrepareIn
// field must be assigned here.
func toErrorReportPrepareIn(in PrepareOrchestratorIn) ErrorReportPrepareIn {
	return ErrorReportPrepareIn{
		Skill:                  in.Skill,
		Step:                   in.Step,
		Operation:              in.Operation,
		Error:                  in.Error,
		ExitOrHTTPCode:         in.ExitOrHTTPCode,
		ErrorType:              in.ErrorType,
		UserIntent:             in.UserIntent,
		ArgsString:             in.ArgsString,
		SuggestedInvestigation: in.SuggestedInvestigation,
	}
}

// ---------------------------------------------------------------------------
// Dispatch
// ---------------------------------------------------------------------------

// prepareOrchestrator is prepare_orchestrator's dispatcher: it switches on
// Mode and delegates to the existing, unchanged hardenPrepare/
// errorReportPrepare core functions. Split out from RegisterPrepareOrchestratorTools
// so the mode-validation and field-mapping logic can be unit tested without
// standing up an MCP server/client.
func prepareOrchestrator(in PrepareOrchestratorIn) (PrepareOrchestratorOut, error) {
	switch in.Mode {
	case "harden":
		root, err := worktree.MainRoot()
		if err != nil {
			return PrepareOrchestratorOut{}, &mcpserver.InfraError{
				Msg:   fmt.Sprintf("resolve project root: %s", err.Error()),
				Cause: err,
			}
		}
		contentRoot := activeWorktreeRootSafe()
		if contentRoot == "" {
			contentRoot = root
		}
		out, err := hardenPrepare(root, contentRoot, toHardenPrepareIn(in))
		if err != nil {
			return PrepareOrchestratorOut{}, err
		}

		// Inject history context into the manifest when historyPath is
		// provided (or auto-resolved from root).
		histPath := in.HistoryPath
		if histPath == "" {
			histPath = filepath.Join(root, paths.DataDir, "history")
		}
		if err := injectHardenHistory(out.ManifestPath, histPath); err != nil {
			// Non-fatal — history is supplementary evidence; log as
			// manifest error but don't block the harden run.
			_ = err
		}

		return PrepareOrchestratorOut{ManifestPath: out.ManifestPath, Mode: in.Mode}, nil

	case "error_report":
		root, err := worktree.MainRoot()
		if err != nil {
			return PrepareOrchestratorOut{}, &mcpserver.InfraError{
				Msg:   fmt.Sprintf("resolve project root: %s", err.Error()),
				Cause: err,
			}
		}
		out, err := errorReportPrepare(root, toErrorReportPrepareIn(in))
		if err != nil {
			return PrepareOrchestratorOut{}, err
		}
		return PrepareOrchestratorOut{ManifestPath: out.ManifestPath, Mode: in.Mode}, nil

	default:
		return PrepareOrchestratorOut{}, &mcpserver.DomainError{
			Msg: fmt.Sprintf("mode: invalid value %q — must be \"harden\" or \"error_report\"", in.Mode),
		}
	}
}

// ---------------------------------------------------------------------------
// History injection for harden manifests
// ---------------------------------------------------------------------------

// injectHardenHistory reads the persistent history store at histPath and
// patches the manifest JSON at manifestPath with a "history" section
// containing recent run records and open deferred issues.
func injectHardenHistory(manifestPath, histPath string) error {
	w := history.NewFileWriter(histPath)

	runs, _ := w.ReadRecentRuns(10)
	deferred, _ := w.ListDeferred()
	openDeferred := history.OpenDeferred(deferred)

	if len(runs) == 0 && len(openDeferred) == 0 {
		return nil // nothing to inject
	}

	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return err
	}

	var manifest map[string]any
	if err := json.Unmarshal(data, &manifest); err != nil {
		return err
	}

	histSection := map[string]any{}
	if len(runs) > 0 {
		histSection["recentRuns"] = runs
	}
	if len(openDeferred) > 0 {
		histSection["openDeferred"] = openDeferred
	}
	manifest["history"] = histSection

	out, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(manifestPath, out, 0o644)
}

// ---------------------------------------------------------------------------
// Registration
// ---------------------------------------------------------------------------

// RegisterPrepareOrchestratorTools registers prepare_orchestrator, replacing
// the former standalone harden_prepare and error_report_prepare tools.
func RegisterPrepareOrchestratorTools(s *mcpserver.Server) {
	mcpserver.Register(s, "prepare_orchestrator",
		"INTERNAL — called by sdlc skills only. Pre-compute either the harden-orchestrator or error-report-orchestrator manifest, selected via mode (\"harden\" or \"error_report\"). harden mode covers failure details, guardrail/dimension/copilot surfaces, pipeline state, and repository context after an SDLC pipeline failure. error_report mode covers calling-skill error context plus repository/branch environment fields for a tooling-error report. Writes the manifest to a temp file and returns its path.",
		func(ctx mcpserver.Ctx, in PrepareOrchestratorIn) (PrepareOrchestratorOut, error) {
			return prepareOrchestrator(in)
		},
	)
}
