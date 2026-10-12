package tools

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	version "github.com/rnagrodzki/sdlc-plugin"
	"github.com/rnagrodzki/sdlc-plugin/internal/execx"
	"github.com/rnagrodzki/sdlc-plugin/internal/fsx"
	"github.com/rnagrodzki/sdlc-plugin/internal/ghx"
	"github.com/rnagrodzki/sdlc-plugin/internal/gitx"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
)

// errorReportTargetRepo is error-report-prepare.js's TARGET_REPO literal.
// Deliberately NOT shared with harden.go's hardenPluginRepoURL constant —
// source keeps the two independent (see harden.go's comment on
// hardenPluginRepoURL); the Go port preserves that separation.
const errorReportTargetRepo = "rnagrodzki/sdlc-plugin"

// ---------------------------------------------------------------------------
// Input / Output
// ---------------------------------------------------------------------------

// ErrorReportPrepareIn is prepare_orchestrator's error_report-mode input. Skill/Step/
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

// errorReportLabels returns the labels of a tooling-error issue for skill:
// the issue the report creates carries them, and the duplicate-issue search
// filters on them.
func errorReportLabels(skill string) []string {
	return []string{"tooling-error", skill}
}

// errorReportIssueList is a seam over ghx.IssueList so tests can stub the
// duplicate-issue search without a real gh binary or network access.
var errorReportIssueList = ghx.IssueList

// errorReportSimilarIssueLimit is the number of open issues that the
// duplicate-issue search asks gh for (the --limit of gh issue list).
const errorReportSimilarIssueLimit = 10

// ErrorReportPrepareOut is errorReportPrepare's result: the path to the
// written manifest (the isolated error-report-orchestrator subagent reads
// every other value from the manifest file itself), plus the duplicate-issue
// search result.
//
// SimilarIssues holds the open tooling-error issues of the target repo whose
// title starts with "[<skill>]". It is never nil: it is [] when nothing
// matches or when the search was skipped. SimilarIssuesNote says why the
// search was skipped and is empty when the search ran. prepare_orchestrator
// forwards both to the skill, together with its own Mode.
type ErrorReportPrepareOut struct {
	ManifestPath      string      `json:"manifestPath"`
	SimilarIssues     []ghx.Issue `json:"similarIssues"`
	SimilarIssuesNote string      `json:"similarIssuesNote,omitempty"`
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
	// Template is the ToolingError.md issue body template text, embedded
	// in the binary. The orchestrator agent fills it from this field
	// because its working directory is the user's project, which has no
	// skills/error-report/ tree.
	Template string `json:"template"`
}

// ---------------------------------------------------------------------------
// errorReportPrepare
// ---------------------------------------------------------------------------

// errorReportPrepare is the Go port of error-report-prepare.js's main().
// Self-contained: no dependency on guardrails.go/harden.go, and (unlike
// harden mode) no KD5 config-version gate.
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
		return ErrorReportPrepareOut{}, &mcpserver.DomainError{
			Msg:        strings.Join(msgs, "; "),
			Suggestion: "Set skill, step, operation, and errorText in the input, then call prepare_orchestrator with mode error_report again.",
		}
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
		Labels:                 errorReportLabels(skill),
		Template:               version.ToolingErrorTemplate,
	}

	tmpDir, err := os.MkdirTemp("", "sdlc-error-report-")
	if err != nil {
		return ErrorReportPrepareOut{}, &mcpserver.InfraError{
			Msg:        fmt.Sprintf("create temp dir: %s", err.Error()),
			Suggestion: "Check that the OS temp directory allows creating new directories, then retry prepare_orchestrator with mode error_report.",
			Cause:      err,
		}
	}
	manifestPath := filepath.Join(tmpDir, "manifest.json")
	if err := fsx.AtomicWriteJSON(manifestPath, manifest); err != nil {
		return ErrorReportPrepareOut{}, &mcpserver.InfraError{
			Msg:        fmt.Sprintf("write manifest: %s", err.Error()),
			Suggestion: "Check available disk space and write permission on the temp directory created just before this step, then retry prepare_orchestrator with mode error_report — it creates a fresh one each call.",
			Cause:      err,
		}
	}

	similar, note := errorReportSimilarIssues(root, skill)
	return ErrorReportPrepareOut{ManifestPath: manifestPath, SimilarIssues: similar, SimilarIssuesNote: note}, nil
}

// errorReportSimilarIssues searches the target repo for open issues that
// already report the same class of error. It runs gh issue list once, with
// the labels "tooling-error" and skill (the label filter runs in gh), then
// keeps the issues whose title starts with "[<skill>]" (the title filter
// runs here, because gh has no title-prefix filter).
//
// The search is advisory, so no failure stops the report. The first result
// is always non-nil. On a failure the issues are [] and the note says why:
//   - gh not installed: "duplicate search skipped: gh not found"
//   - gh exits non-zero: "duplicate search skipped: gh issue list failed:
//     <first line of stderr>", or the exit code when stderr is empty
//   - gh output is not JSON: "duplicate search skipped: gh issue list
//     output is not JSON"
//   - any other failure (for example an output-cap overflow): "duplicate
//     search skipped: gh issue list failed: <first line of the error>"
//
// This is the only gh call of error_report mode. The other gh call of
// prepare_orchestrator is gh issue view in harden mode (--from-issue), which
// turns a failure into an InfraError with a suggestion to check gh auth
// status. Here a failure becomes a note instead, and the note carries the
// first line of the gh error, which names an authentication or permission
// failure. The search reads a public repo, so no account switch is offered.
func errorReportSimilarIssues(root, skill string) ([]ghx.Issue, string) {
	issues, err := errorReportIssueList(root, errorReportTargetRepo, errorReportLabels(skill), errorReportSimilarIssueLimit)
	if err != nil {
		return []ghx.Issue{}, errorReportSearchNote(err)
	}
	prefix := "[" + skill + "]"
	similar := []ghx.Issue{}
	for _, issue := range issues {
		if strings.HasPrefix(issue.Title, prefix) {
			similar = append(similar, issue)
		}
	}
	return similar, ""
}

// errorReportSearchNote turns a failure of the duplicate-issue search into the
// note text that explains why the search was skipped.
func errorReportSearchNote(err error) string {
	const skipped = "duplicate search skipped: "
	var exitErr *ghx.ExitError
	switch {
	case errors.Is(err, ghx.ErrGHNotFound):
		return skipped + "gh not found"
	case errors.Is(err, ghx.ErrNotJSON):
		return skipped + "gh issue list output is not JSON"
	case errors.As(err, &exitErr):
		reason := dashboardFirstLine(strings.TrimSpace(exitErr.Stderr))
		if reason == "" {
			reason = fmt.Sprintf("exit code %d", exitErr.ExitCode)
		}
		return skipped + "gh issue list failed: " + reason
	default:
		return skipped + "gh issue list failed: " + dashboardFirstLine(strings.TrimSpace(err.Error()))
	}
}
