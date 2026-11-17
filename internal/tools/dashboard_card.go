package tools

import (
	_ "embed"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
	"github.com/rnagrodzki/sdlc-plugin/internal/state"
	"github.com/rnagrodzki/sdlc-plugin/internal/t3"
)

// ---------------------------------------------------------------------------
// dashboard_card tool
// ---------------------------------------------------------------------------

// DashboardCardIn is the input for the dashboard_card tool.
type DashboardCardIn struct {
	RunID string `json:"runId" jsonschema_description:"Plain text: the state file name of a ship or execute run, without .json: ship- or execute-, then 1 to 200 characters of A-Z a-z 0-9 . _ -. A plan- run id is refused. Example: ship-feat-x-20261116T175538Z"`
	Final bool   `json:"final,omitempty" jsonschema_description:"Plain JSON bool. true for the end card: the view does not refresh, and a run whose state still says running shows as stopped, resumable. Omitted: the start card, which refreshes while the run runs. Example: true"`
}

// DashboardCardStep is one step of the run that the card shows.
type DashboardCardStep struct {
	Name   string `json:"name" jsonschema_description:"Step name: a ship step, or wave <n> for an execute run."`
	Status string `json:"status" jsonschema_description:"Step status as the run state stores it, for example pending, in_progress, completed, skipped or failed."`
}

// DashboardCardOut is the output of the dashboard_card tool. A tool
// registered with RegisterUI also returns it as structuredContent.
type DashboardCardOut struct {
	Summary     string              `json:"summary" jsonschema_description:"One line: the kind, run id, branch and status of the run."`
	RunID       string              `json:"runId" jsonschema_description:"The run id of the input."`
	Kind        string              `json:"kind" jsonschema_description:"ship or execute."`
	Branch      string              `json:"branch" jsonschema_description:"Branch of the run."`
	Status      string              `json:"status" jsonschema_description:"Run status: running, completed or failed."`
	Final       bool                `json:"final" jsonschema_description:"Copy of the final input: true for the end card."`
	T3ThreadID  string              `json:"t3ThreadId,omitempty" jsonschema_description:"T3 Code thread id stored in the run state. Absent when the run has none."`
	StartedAt   string              `json:"startedAt" jsonschema_description:"Start time of the run, RFC 3339."`
	CompletedAt *string             `json:"completedAt" jsonschema_description:"End time of a completed run, RFC 3339. Empty while the run has no end time."`
	Steps       []DashboardCardStep `json:"steps" jsonschema_description:"Steps of the run in order. Empty when the run has no step yet."`
	Issues      map[string]int      `json:"issues" jsonschema_description:"Count of the issues of the run for each severity. Empty when the run has no issue."`
	Next        string              `json:"next" jsonschema_description:"What the caller does next."`
}

// dashboardCardMaxHTML is the size limit of dashboard_card.html (64 KiB).
const dashboardCardMaxHTML = 65536

// dashboardCardToolName is the registered name of the tool. The view calls
// the tool again by this name to refresh.
const dashboardCardToolName = "dashboard_card"

// dashboardCardViewURI is the ui:// address of the card view.
const dashboardCardViewURI = "ui://sdlc/dashboard-card"

// dashboardCardHTML is the card view: one self-contained HTML file, because
// an MCP Apps host such as T3 Code can block every asset and network call of
// a view.
//
//go:embed dashboard_card.html
var dashboardCardHTML string

// dashboardCardNetworkRe matches the view text that loads an asset or calls
// the network: a src attribute, an href to an http, https or
// protocol-relative address, a fetch call, or XMLHttpRequest.
var dashboardCardNetworkRe = regexp.MustCompile(`(?i)\bsrc\s*=|\bhref\s*=\s*["']?\s*(https?:|//)|\bfetch\s*\(|XMLHttpRequest`)

// dashboardCardDescription is the MCP description of dashboard_card.
const dashboardCardDescription = "Show one ship or execute run as an MCP App card (view ui://sdlc/dashboard-card) in a client that supports MCP Apps. Other clients get the same run summary as text. Requires: runId. Reads the run state files in <main-worktree>/.sdlc-v2/runs and writes nothing. Call it at the start and end of a top-level ship run or a standalone execute run. When next says this client shows no card, do not call it again in that run. Errors: DomainError for a bad runId, a missing run, or a run of another T3 thread. DataError when run files cannot be read. InfraError when the runs folder cannot be listed."

// dashboardCardCheckView returns an error when html is larger than
// dashboardCardMaxHTML or has text that dashboardCardNetworkRe matches.
func dashboardCardCheckView(html string) error {
	if len(html) > dashboardCardMaxHTML {
		return fmt.Errorf("dashboard_card view is %d bytes, above the limit of %d", len(html), dashboardCardMaxHTML)
	}
	if m := dashboardCardNetworkRe.FindString(html); m != "" {
		return fmt.Errorf("dashboard_card view loads an asset or calls the network: %q", m)
	}
	return nil
}

// dashboardCardPlanPrefix is the run id prefix of a plan run. runIDRe accepts
// it, and the card refuses it.
const dashboardCardPlanPrefix = "plan-"

// dashboardCardSlugLen returns the byte length of runID after its ship-,
// execute- or plan- prefix, or of the whole runID when it has none of them.
func dashboardCardSlugLen(runID string) int {
	for _, prefix := range []string{"ship-", "execute-", dashboardCardPlanPrefix} {
		if rest, ok := strings.CutPrefix(runID, prefix); ok {
			return len(rest)
		}
	}
	return len(runID)
}

// dashboardCardCheckRunID returns a DomainError when runID is not the name of
// a ship or execute run.
func dashboardCardCheckRunID(runID string) error {
	if runIDRe.MatchString(runID) && !strings.HasPrefix(runID, dashboardCardPlanPrefix) {
		return nil
	}
	return &mcpserver.DomainError{
		Msg: fmt.Sprintf("runId %s is not a ship or execute run name: ship- or execute-, then 1 to %d characters of A-Z a-z 0-9 . _ - (got %d characters after the prefix)",
			runID, t3.MaxRunSlugLen, dashboardCardSlugLen(runID)),
		Suggestion: "Pass the state file name of a ship or execute run without .json, for example ship-feat-x-20261116T175538Z.",
	}
}

// dashboardCardFind returns the run of root whose state.RunID is runID. The
// error is an InfraError when the runs folder cannot be listed, a DataError
// when the run is missing and some run files could not be read, and a
// DomainError when the run is missing and every run file was read.
func dashboardCardFind(root, runID string) (*state.State, error) {
	runsDir := filepath.Join(root, paths.DataDir, paths.RunsSubdir)
	list, err := state.List(root)
	if err != nil {
		return nil, &mcpserver.InfraError{
			Msg:        fmt.Sprintf("cannot list runs: %s", err.Error()),
			Suggestion: fmt.Sprintf("Check that %s is a readable folder, then call again.", runsDir),
			Cause:      err,
		}
	}
	for _, st := range list.States {
		if state.RunID(st) == runID {
			return st, nil
		}
	}
	if list.Skipped > 0 {
		return nil, &mcpserver.DataError{
			Msg:        fmt.Sprintf("run %s not found — %d run files could not be read", runID, list.Skipped),
			Suggestion: fmt.Sprintf("Open the run files in %s and check their JSON. The dashboard skips the same files.", runsDir),
		}
	}
	return nil, &mcpserver.DomainError{
		Msg:        fmt.Sprintf("run %s not found", runID),
		Suggestion: "Use the stateFile of ship_prepare or the filePath of execute_state init, without .json.",
	}
}

// dashboardCardNoView is the Next text for a client that shows no MCP App
// view. The ship and execute skills match its first sentence.
const dashboardCardNoView = "This client shows no card. The text is the run summary. Do not call dashboard_card again in this run."

// dashboardCardShowsView reports whether the client of the call can show the
// card view. A client that announces the MCP Apps extension
// (mcpserver.MCPAppsExtension) can. A process in a T3 Code thread (threadID
// non-empty) also counts as one: T3 Code renders the card (manual check of
// 2026-11-17), but which capabilities T3 Code announces in initialize is not
// verified. Without this fallback a T3 client that does not announce the
// extension would get the no-view text, and the skills would stop calling
// the card there.
func dashboardCardShowsView(ctx mcpserver.Ctx, threadID string) bool {
	return ctx.SupportsMCPApps() || threadID != ""
}

// dashboardCard builds the card of run in.RunID under root. threadID is the
// T3 Code thread id of this process, "" outside T3 Code. A run that stores
// another thread id is refused only when threadID is set. showsView is true
// when the client can show the card view; it selects the Next text.
func dashboardCard(root, threadID string, showsView bool, in DashboardCardIn) (DashboardCardOut, error) {
	if err := dashboardCardCheckRunID(in.RunID); err != nil {
		return DashboardCardOut{}, err
	}
	st, err := dashboardCardFind(root, in.RunID)
	if err != nil {
		return DashboardCardOut{}, err
	}
	// The prefix is ship or execute (checked above), so ok is always true.
	p, _, _ := dashboardStatePipeline(st, nil)
	if threadID != "" && p.T3ThreadID != "" && p.T3ThreadID != threadID {
		return DashboardCardOut{}, &mcpserver.DomainError{
			Msg:        fmt.Sprintf("run %s belongs to another T3 thread", in.RunID),
			Suggestion: "Show only a run of this thread. Continue the current step.",
		}
	}

	out := DashboardCardOut{
		Summary:     fmt.Sprintf("%s run %s on branch %s is %s.", p.Kind, in.RunID, p.Branch, p.Status),
		RunID:       in.RunID,
		Kind:        p.Kind,
		Branch:      p.Branch,
		Status:      p.Status,
		Final:       in.Final,
		T3ThreadID:  p.T3ThreadID,
		StartedAt:   p.StartedAt,
		CompletedAt: p.CompletedAt,
		Steps:       make([]DashboardCardStep, 0, len(p.Steps)),
		Issues:      map[string]int{},
	}
	for _, s := range p.Steps {
		out.Steps = append(out.Steps, DashboardCardStep{Name: s.Name, Status: s.Status})
	}
	for _, issue := range p.Issues {
		out.Issues[issue.Severity]++
	}
	if showsView {
		out.Next = fmt.Sprintf("Card shows run %s (%s). Continue the current step.", in.RunID, p.Status)
	} else {
		out.Next = dashboardCardNoView
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Registration
// ---------------------------------------------------------------------------

// RegisterDashboardCardTools registers the dashboard_card tool and its MCP
// App view on the server. It panics when the embedded view fails
// dashboardCardCheckView.
func RegisterDashboardCardTools(s *mcpserver.Server) {
	if err := dashboardCardCheckView(dashboardCardHTML); err != nil {
		panic("tools: register dashboard_card: " + err.Error())
	}
	mcpserver.RegisterUI(s, dashboardCardToolName,
		dashboardCardDescription,
		mcpserver.Annotations{
			Title:       "Show a run card",
			ReadOnly:    true,
			Destructive: false,
			Idempotent:  true,
			OpenWorld:   false,
		},
		mcpserver.UIResource{URI: dashboardCardViewURI, Name: "dashboard-card", HTML: dashboardCardHTML},
		func(ctx mcpserver.Ctx, in DashboardCardIn) (DashboardCardOut, error) {
			// Same root as the dashboard tool: the main worktree, so a
			// linked worktree reads the runs of the main worktree.
			root, err := dashboardRoot()
			if err != nil {
				return DashboardCardOut{}, err
			}
			threadID := t3.ThreadID()
			return dashboardCard(root, threadID, dashboardCardShowsView(ctx, threadID), in)
		},
	)
}
