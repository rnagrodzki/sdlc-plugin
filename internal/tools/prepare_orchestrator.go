package tools

import (
	"fmt"

	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
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
	Mode string `json:"mode"`

	// --- shared across both modes ---
	Skill      string `json:"skill"`
	Step       string `json:"step,omitempty"`
	Operation  string `json:"operation,omitempty"`
	ErrorType  string `json:"errorType,omitempty"`
	UserIntent string `json:"userIntent,omitempty"`
	ArgsString string `json:"argsString,omitempty"`

	// --- harden mode only (HardenPrepareIn) ---
	FailureText     string `json:"failureText,omitempty"`
	ExitCode        string `json:"exitCode,omitempty"`
	FromIssue       string `json:"fromIssue,omitempty"`
	SkipConfigCheck bool   `json:"skipConfigCheck,omitempty"`

	// --- error_report mode only (ErrorReportPrepareIn) ---
	Error                  string `json:"errorText,omitempty"`
	ExitOrHTTPCode         string `json:"exitOrHttpCode,omitempty"`
	SuggestedInvestigation string `json:"suggestedInvestigation,omitempty"`
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
