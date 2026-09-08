package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/execx"
	"github.com/rnagrodzki/sdlc-plugin/internal/fsx"
	"github.com/rnagrodzki/sdlc-plugin/internal/gitx"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
)

// errorReportTargetRepo is error-report-prepare.js's TARGET_REPO literal.
// Deliberately NOT shared with harden.go's hardenPluginRepoURL constant —
// source keeps the two independent (see harden.go's comment on
// hardenPluginRepoURL); the Go port preserves that separation.
const errorReportTargetRepo = "rnagrodzki/sdlc-marketplace"

// ---------------------------------------------------------------------------
// Input / Output
// ---------------------------------------------------------------------------

// ErrorReportPrepareIn is error_report_prepare's input. Skill/Step/
// Operation/Error are required; the rest are optional with empty-string
// defaults, matching source's `!= null` checks.
//
// Deliberately has NO SkipConfigCheck field: source (error-report-prepare.js)
// contains no reference anywhere to skipConfigCheck/ensureConfigVersion —
// a confirmed, deliberate omission (see fact sheet Notes), not an oversight.
type ErrorReportPrepareIn struct {
	Skill     string `json:"skill"`
	Step      string `json:"step"`
	Operation string `json:"operation"`
	// Error is source's required `errorText` field. Named Error in Go (per
	// the plan's Contract line) but tagged json:"errorText" so the wire
	// schema matches source's field name rather than silently renaming it.
	Error string `json:"errorText"`

	ExitOrHTTPCode         string `json:"exitOrHttpCode,omitempty"`
	ErrorType              string `json:"errorType,omitempty"`
	UserIntent             string `json:"userIntent,omitempty"`
	ArgsString             string `json:"argsString,omitempty"`
	SuggestedInvestigation string `json:"suggestedInvestigation,omitempty"`
}

// ErrorReportPrepareOut is error_report_prepare's output: the path to the
// written manifest (KD4 file handoff).
type ErrorReportPrepareOut struct {
	ManifestPath string `json:"manifestPath"`
}

// ---------------------------------------------------------------------------
// Manifest shape (source: error-report-prepare.js manifest object)
// ---------------------------------------------------------------------------

type errorReportManifest struct {
	Skill                  string   `json:"skill"`
	Step                   string   `json:"step"`
	Operation              string   `json:"operation"`
	ErrorText              string   `json:"errorText"`
	ExitOrHTTPCode         string   `json:"exitOrHttpCode"`
	ErrorType              string   `json:"errorType"`
	UserIntent             string   `json:"userIntent"`
	ArgsString             string   `json:"argsString"`
	SuggestedInvestigation string   `json:"suggestedInvestigation"`
	Repository             string   `json:"repository"`
	CurrentBranch          string   `json:"currentBranch"`
	Timestamp              string   `json:"timestamp"`
	TargetRepo             string   `json:"targetRepo"`
	Labels                 []string `json:"labels"`
}

// ---------------------------------------------------------------------------
// errorReportPrepare
// ---------------------------------------------------------------------------

// errorReportPrepare is the Go port of error-report-prepare.js's main().
// Self-contained: no dependency on guardrails.go/harden.go, and (unlike
// harden_prepare) no KD5 config-version gate.
//
// Source's detectRepository/detectCurrentBranch (safeExec with no explicit
// cwd) inherit whatever directory the script process was launched from.
// There is no equivalent "launch directory" for an MCP server process, so
// root (resolved by the prepare_orchestrator registration's error_report
// branch via worktree.MainRoot, the same anchor every other tool in this
// package uses) scopes both git commands here instead.
func errorReportPrepare(root string, in ErrorReportPrepareIn) (ErrorReportPrepareOut, error) {
	var missing []string
	if strings.TrimSpace(in.Skill) == "" {
		missing = append(missing, "skill")
	}
	if strings.TrimSpace(in.Step) == "" {
		missing = append(missing, "step")
	}
	if strings.TrimSpace(in.Operation) == "" {
		missing = append(missing, "operation")
	}
	if strings.TrimSpace(in.Error) == "" {
		missing = append(missing, "errorText")
	}
	if len(missing) > 0 {
		msgs := make([]string, len(missing))
		for i, m := range missing {
			msgs[i] = "Missing required field: " + m
		}
		return ErrorReportPrepareOut{}, &mcpserver.DomainError{Msg: strings.Join(msgs, "; ")}
	}

	skill := strings.TrimSpace(in.Skill)

	repository, _ := execx.Run("git", []string{"remote", "get-url", "origin"}, execx.Options{Dir: root})
	currentBranch, _ := gitx.CurrentBranch(root)

	manifest := errorReportManifest{
		Skill:                  skill,
		Step:                   strings.TrimSpace(in.Step),
		Operation:              strings.TrimSpace(in.Operation),
		ErrorText:              in.Error,
		ExitOrHTTPCode:         in.ExitOrHTTPCode,
		ErrorType:              strings.TrimSpace(in.ErrorType),
		UserIntent:             in.UserIntent,
		ArgsString:             in.ArgsString,
		SuggestedInvestigation: in.SuggestedInvestigation,
		Repository:             repository,
		CurrentBranch:          currentBranch,
		Timestamp:              time.Now().UTC().Format(time.RFC3339),
		TargetRepo:             errorReportTargetRepo,
		Labels:                 []string{"tooling-error", skill},
	}

	tmpDir, err := os.MkdirTemp("", "sdlc-error-report-")
	if err != nil {
		return ErrorReportPrepareOut{}, &mcpserver.InfraError{Msg: fmt.Sprintf("create temp dir: %s", err.Error()), Cause: err}
	}
	manifestPath := filepath.Join(tmpDir, "manifest.json")
	if err := fsx.AtomicWriteJSON(manifestPath, manifest); err != nil {
		return ErrorReportPrepareOut{}, &mcpserver.InfraError{Msg: fmt.Sprintf("write manifest: %s", err.Error()), Cause: err}
	}

	return ErrorReportPrepareOut{ManifestPath: manifestPath}, nil
}
