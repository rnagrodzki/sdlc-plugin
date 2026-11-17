package tools

import (
	_ "embed"
	"fmt"
	"maps"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/rnagrodzki/sdlc-plugin/internal/dashboard"
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
	RunID    string `json:"runId" jsonschema_description:"Plain text: the state file name of a ship or execute run, without .json: ship- or execute-, then 1 to 200 characters of A-Z a-z 0-9 . _ -. A plan- run id is refused. Example: ship-feat-x-20261116T175538Z"`
	Final    bool   `json:"final,omitempty" jsonschema_description:"Plain JSON bool. true for the end card: the view does not refresh, and a run whose state still says running shows as stopped, resumable. Omitted: the start card, which refreshes while the run runs. Example: true"`
	Snapshot bool   `json:"snapshot,omitempty" jsonschema_description:"Plain JSON bool. true for a middle card: the view draws the run once and does not refresh. final wins when both are true. Omitted: a live card. Example: true"`
}

// DashboardCardStep is one step of the run that the card shows.
type DashboardCardStep struct {
	Name       string              `json:"name" jsonschema_description:"Step name: a ship step, or wave <n> for an execute run."`
	Status     string              `json:"status" jsonschema_description:"Step status as the run state stores it, for example pending, in_progress, completed, skipped or failed."`
	TasksDone  *int                `json:"tasksDone,omitempty" jsonschema_description:"Count of the tasks of the wave with status completed. Only on a wave <n> step of an execute run."`
	TasksTotal *int                `json:"tasksTotal,omitempty" jsonschema_description:"Count of all tasks of the wave. Only on a wave <n> step of an execute run."`
	Waves      []DashboardCardWave `json:"waves,omitempty" jsonschema_description:"Waves of the execute run that the dashboard joins to the ship run. Only on the execute step of a ship run, and only when an execute run is joined."`
}

// DashboardCardWave is one wave of the execute run joined to a ship run. It
// is a flat type, not a DashboardCardStep: a []DashboardCardStep field inside
// DashboardCardStep is a recursive type, and the schema inference of
// RegisterUI fails on a cycle.
type DashboardCardWave struct {
	Name       string `json:"name" jsonschema_description:"wave <n>: the same string as the name of the wave step in the card of the joined execute run. Example: wave 2"`
	Status     string `json:"status" jsonschema_description:"Wave status: the same value as the status of that wave step, for example pending, in_progress, completed or failed."`
	TasksDone  int    `json:"tasksDone" jsonschema_description:"Count of the tasks of the wave with status completed: the same value as tasksDone of that wave step."`
	TasksTotal int    `json:"tasksTotal" jsonschema_description:"Count of all tasks of the wave: the same value as tasksTotal of that wave step."`
}

// DashboardCardOut is the output of the dashboard_card tool. A tool
// registered with RegisterUI also returns it as structuredContent.
type DashboardCardOut struct {
	Summary      string              `json:"summary" jsonschema_description:"One line: the kind, run id, branch and status of the run."`
	RunID        string              `json:"runId" jsonschema_description:"The run id of the input."`
	Kind         string              `json:"kind" jsonschema_description:"ship or execute."`
	Branch       string              `json:"branch" jsonschema_description:"Branch of the run."`
	Status       string              `json:"status" jsonschema_description:"Run status as the run state stores it: running, completed or failed. The end card (final true) shows a running run as stopped, resumable; this field still says running."`
	Final        bool                `json:"final" jsonschema_description:"Copy of the final input: true for the end card."`
	Snapshot     bool                `json:"snapshot,omitempty" jsonschema_description:"true for a middle card that the view draws once and does not refresh. Absent unless true: absent when snapshot was not sent, and absent when final is true, because final wins."`
	T3ThreadID   string              `json:"t3ThreadId,omitempty" jsonschema_description:"T3 Code thread id stored in the run state. Absent when the run has none."`
	StartedAt    string              `json:"startedAt" jsonschema_description:"Start time of the run, RFC 3339."`
	CompletedAt  *string             `json:"completedAt" jsonschema_description:"End time of a completed run, RFC 3339. null while the run has no end time; the text renders it as (none)."`
	Steps        []DashboardCardStep `json:"steps" jsonschema_description:"Steps of the run in order. Empty when the run has no step yet."`
	Issues       map[string]int      `json:"issues" jsonschema_description:"Count of the issues of the run for each severity. Empty when the run has no issue."`
	Next         string              `json:"next" jsonschema_description:"What the caller does next."`
	Text         string              `json:"text,omitempty" jsonschema_description:"Client with no card only: the run card as a fixed-width text block. Print it in the chat unchanged, in a code block. Its last line is Dashboard: <url>#<runId> when the dashboard runs, and Dashboard: link unavailable when the server record cannot be read; with no server it has no Dashboard line."`
	DashboardURL string              `json:"dashboardUrl,omitempty" jsonschema_description:"Client with no card only, when the dashboard server runs: the link to the run block, <url>#<runId>."`
}

// dashboardCardMaxHTML is the size limit of dashboard_card.html. It is
// lower than mcpserver.MaxUIHTMLBytes, the limit of every view.
const dashboardCardMaxHTML = 64 << 10

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
const dashboardCardDescription = "Show one ship or execute run as an MCP App card (view ui://sdlc/dashboard-card) in a client that supports MCP Apps. A process in a T3 Code thread (T3CODE_THREAD_ID set) also counts as such a client, whatever capabilities the client announces. Other clients get the card as a text block, plus the dashboard link of the run when the dashboard runs. Requires: runId. Optional: final (end card), snapshot (middle card, no refresh). Reads the run state files in <main-worktree>/.sdlc-v2/runs and the dashboard server record, probes that server over loopback HTTP, and writes nothing. Call it at each card point of a top-level ship run or a standalone execute run. Errors: DomainError for a bad runId, a missing run, or a run of another T3 thread. DataError when the run is not found and some run files could not be read, or, in a T3 Code thread, when the run stores a t3ThreadId that is not a string. InfraError when the runs folder cannot be listed. A dashboard server record that cannot be read is not an error: the text card says that the link is unavailable."

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

// dashboardCardCheckRunID returns a DomainError when runID is not the name of
// a ship or execute run.
func dashboardCardCheckRunID(runID string) error {
	if runIDRe.MatchString(runID) && !strings.HasPrefix(runID, dashboardCardPlanPrefix) {
		return nil
	}
	return &mcpserver.DomainError{
		Msg: fmt.Sprintf("runId %s is not a ship or execute run name: ship- or execute-, then 1 to %d characters of A-Z a-z 0-9 . _ - (got %d characters after the prefix)",
			runID, t3.MaxRunSlugLen, runIDSlugLen(runID)),
		Suggestion: "Pass the state file name of a ship or execute run without .json, for example ship-feat-x-20261116T175538Z.",
	}
}

// dashboardCardFind returns the run of root whose state.RunID is runID, and
// every state that the one state.List call read, so the caller can join runs
// without a second list. The error is an InfraError when the runs folder
// cannot be listed, a DataError when the run is missing and some run files
// could not be read, and a DomainError when the run is missing and every run
// file was read.
func dashboardCardFind(root, runID string) (*state.State, []*state.State, error) {
	runsDir := filepath.Join(root, paths.DataDir, paths.RunsSubdir)
	list, err := state.List(root)
	if err != nil {
		return nil, nil, &mcpserver.InfraError{
			Msg:        fmt.Sprintf("cannot list runs: %s", err.Error()),
			Suggestion: fmt.Sprintf("Check that %s is a readable folder, then call again.", runsDir),
			Cause:      err,
		}
	}
	for _, st := range list.States {
		if state.RunID(st) == runID {
			return st, list.States, nil
		}
	}
	// state.List returns only a count of the unreadable files, not their
	// names or causes, so the error names the runs folder. When the run is
	// found, the unreadable files do not matter to the card and are ignored.
	if list.Skipped > 0 {
		return nil, nil, &mcpserver.DataError{
			Msg:        fmt.Sprintf("run %s not found — %d run files could not be read", runID, list.Skipped),
			Suggestion: fmt.Sprintf("Open the run files in %s and check their JSON. The dashboard skips the same files.", runsDir),
		}
	}
	return nil, nil, &mcpserver.DomainError{
		Msg:        fmt.Sprintf("run %s not found", runID),
		Suggestion: "Use the stateFile of ship_prepare or the filePath of execute_state init, without .json.",
	}
}

// The Next texts for a client that shows no MCP App view are built from these
// sentences. The ship and execute skills match dashboardCardNoViewLead, the
// first sentence of each text, so every text starts with it.
const (
	dashboardCardNoViewLead   = "This client shows no card. Print the text field in the chat unchanged, in a code block."
	dashboardCardNoViewTail   = "Call dashboard_card again at the next card point of this run."
	dashboardCardNoViewEnsure = "No dashboard runs: call dashboard({action:\"ensure\", runId:\"%s\"}), then print its url with #%s."
	dashboardCardNoViewNoLink = "The dashboard server record could not be read: the text has no link."
)

// dashboardCardNoView is the Next text for a client that shows no MCP App
// view, when the dashboard server runs.
const dashboardCardNoView = dashboardCardNoViewLead + " " + dashboardCardNoViewTail

// dashboardCardNoViewNoServer is the Next text for a client that shows no MCP
// App view, when no dashboard server runs. Its two %s verbs are the run id.
const dashboardCardNoViewNoServer = dashboardCardNoViewLead + " " + dashboardCardNoViewEnsure + " " + dashboardCardNoViewTail

// dashboardCardNoViewUnknown is the Next text for a client that shows no MCP
// App view, when the dashboard server record cannot be read. It orders no
// ensure: a server can run while only its record read fails.
const dashboardCardNoViewUnknown = dashboardCardNoViewLead + " " + dashboardCardNoViewNoLink + " " + dashboardCardNoViewTail

// dashboardCardSnapshotIgnored is added to Next when the call sent both final
// and snapshot: final wins, and the caller learns that snapshot was dropped.
const dashboardCardSnapshotIgnored = " snapshot was ignored because final is true."

// dashboardCardTextNoLink is the last line of the text block when the
// dashboard server record cannot be read.
const dashboardCardTextNoLink = "Dashboard: link unavailable"

// dashboardCardServer is what the card knows about the dashboard server.
type dashboardCardServer int

const (
	// dashboardCardServerNone: no server runs (no record, or a record of a
	// server that does not answer).
	dashboardCardServerNone dashboardCardServer = iota
	// dashboardCardServerRunning: a server answers; the URL is set.
	dashboardCardServerRunning
	// dashboardCardServerUnknown: the server record cannot be read, so the
	// card cannot tell whether a server runs.
	dashboardCardServerUnknown
)

// dashboardCardServerStatus is the call to dashboard.Status. It is a
// package-level seam, like dashboardDefaultDeps, so tests never reach a real
// server.
var dashboardCardServerStatus = func() (dashboard.Result, error) {
	return dashboard.Status(dashboardDefaultDeps())
}

// dashboardCardServerURL returns the address of the running dashboard server
// and what the card knows about the server. A card error must never stop a
// run, so a failed read of the server record is not returned as an error:
// dashboard.Status returns a missing record as no error, so any error here is
// a record that exists and cannot be read, and the server state is unknown.
func dashboardCardServerURL() (string, dashboardCardServer) {
	res, err := dashboardCardServerStatus()
	if err != nil {
		return "", dashboardCardServerUnknown
	}
	return dashboardCardServerFrom(res)
}

// dashboardCardServerFrom maps a dashboard.Result with no error to the address
// of a running server.
func dashboardCardServerFrom(res dashboard.Result) (string, dashboardCardServer) {
	if !res.Running || res.URL == "" {
		return "", dashboardCardServerNone
	}
	return res.URL, dashboardCardServerRunning
}

// dashboardCardShowsView reports whether the client of the call can show the
// card view. A client that announces the MCP Apps extension
// (mcpserver.MCPAppsExtension) can. A process in a T3 Code thread (threadID
// non-empty) also counts as one: T3 Code renders the card (manual check of
// 2026-11-17), but which capabilities T3 Code announces in initialize is not
// verified. Without this fallback a T3 client that does not announce the
// extension would get the text block printed in the chat at every card
// point, in place of the card.
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
	st, all, err := dashboardCardFind(root, in.RunID)
	if err != nil {
		return DashboardCardOut{}, err
	}
	// In a T3 Code thread, a t3ThreadId that is not a string would read as
	// "no thread" and skip the thread guard below, so it is refused.
	if raw, ok := st.Data[t3.StateKey]; ok && threadID != "" {
		if _, isString := raw.(string); !isString {
			return DashboardCardOut{}, &mcpserver.DataError{
				Msg:        fmt.Sprintf("run %s has %s of type %T, expected string", in.RunID, t3.StateKey, raw),
				Suggestion: fmt.Sprintf("Fix %s in %s: a thread id string, or remove the key. Continue the current step.", t3.StateKey, st.Path),
			}
		}
	}
	// The prefix is ship or execute (checked above), so ok is always true.
	p, _, _ := dashboardStatePipeline(st, nil)
	if threadID != "" && p.T3ThreadID != "" && p.T3ThreadID != threadID {
		return DashboardCardOut{}, &mcpserver.DomainError{
			Msg:        fmt.Sprintf("run %s belongs to another T3 thread", in.RunID),
			Suggestion: "Pass the runId of a run of this thread: the stateFile of ship_prepare or the filePath of execute_state init, without .json. Continue the current step.",
		}
	}

	out := DashboardCardOut{
		Summary:     fmt.Sprintf("%s run %s on branch %s is %s.", p.Kind, in.RunID, p.Branch, p.Status),
		RunID:       in.RunID,
		Kind:        p.Kind,
		Branch:      p.Branch,
		Status:      p.Status,
		Final:       in.Final,
		Snapshot:    in.Snapshot && !in.Final, // final wins
		T3ThreadID:  p.T3ThreadID,
		StartedAt:   p.StartedAt,
		CompletedAt: p.CompletedAt,
		Steps:       dashboardCardSteps(p),
		Issues:      map[string]int{},
	}
	// The issue counts come from the run itself. The join below adds no
	// issue of the joined execute run to the ship card.
	for _, issue := range p.Issues {
		out.Issues[issue.Severity]++
	}
	if p.Kind == dashboardRunKindShip {
		if waves := dashboardCardShipWaves(p, all); waves != nil {
			for i := range out.Steps {
				if out.Steps[i].Name == dashboardShipStepExecute {
					out.Steps[i].Waves = waves
					break
				}
			}
		}
	}
	// note tells the caller that a snapshot request was dropped: final wins.
	note := ""
	if in.Snapshot && in.Final {
		note = dashboardCardSnapshotIgnored
	}
	if showsView {
		out.Next = fmt.Sprintf("Card shows run %s (%s). Continue the current step.", in.RunID, p.Status) + note
		return out, nil
	}
	// A client with no card gets the card as text, with the link to the run
	// when the dashboard runs.
	url, server := dashboardCardServerURL()
	switch server {
	case dashboardCardServerRunning:
		out.DashboardURL = url + "#" + in.RunID
		out.Next = dashboardCardNoView
	case dashboardCardServerUnknown:
		out.Next = dashboardCardNoViewUnknown
	default:
		out.Next = fmt.Sprintf(dashboardCardNoViewNoServer, in.RunID, in.RunID)
	}
	out.Next += note
	out.Text = dashboardCardText(out, server)
	return out, nil
}

// The layout of the text block. A step row is the step indent, the marker, a
// space, the name padded to dashboardCardTextNameWidth (a longer name is not
// cut), a space, then the columns (task count, status) separated by
// dashboardCardTextGap. A wave row of a ship run is the wave indent, the wave
// name, then the same columns, each after dashboardCardTextGap.
const (
	dashboardCardTextNameWidth   = 18
	dashboardCardTextHeaderWidth = 40 // the STEPS header is padded to this width before its count
	dashboardCardTextStepIndent  = "  "
	dashboardCardTextWaveIndent  = "        "
	dashboardCardTextGap         = "  "
)

// dashboardCardTextNotRecorded stands for an empty branch or time.
const dashboardCardTextNotRecorded = "not recorded"

// dashboardCardMarker returns the marker and the bar character of a step
// status.
func dashboardCardMarker(status string) (marker string, bar byte) {
	switch status {
	case StepCompleted:
		return "[x]", '#'
	case StepInProgress:
		return "[>]", '>'
	case StepSkipped:
		return "[-]", '#'
	case StepFailed:
		return "[!]", '!'
	}
	return "[ ]", '.'
}

// dashboardCardText renders out as the fixed-width text block for a client
// with no card. server is what the card knows about the dashboard server. The
// last line is the dashboard link of out.DashboardURL when the server runs,
// dashboardCardTextNoLink when its record cannot be read, and the ISSUES line
// when no server runs: the caller then starts the server and prints the link
// itself. The block has no trailing newline.
func dashboardCardText(out DashboardCardOut, server dashboardCardServer) string {
	orNotRecorded := func(v string) string {
		if v == "" {
			return dashboardCardTextNotRecorded
		}
		return v
	}
	shownStatus := out.Status
	if out.Final && out.Status == PipelineRunning {
		shownStatus = "stopped"
	}
	completed := ""
	if out.CompletedAt != nil {
		completed = *out.CompletedAt
	}
	var b strings.Builder
	line := func(format string, args ...any) { fmt.Fprintf(&b, format+"\n", args...) }
	line("%s  [%s]", strings.ToUpper(out.Kind), shownStatus)
	line("%s", out.RunID)
	line("Branch:    %s", orNotRecorded(out.Branch))
	line("Started:   %s", orNotRecorded(out.StartedAt))
	line("Completed: %s", orNotRecorded(completed))
	line("")
	if len(out.Steps) == 0 {
		line("STEPS")
		line("No steps yet.")
	} else {
		done := 0
		bar := make([]byte, 0, len(out.Steps))
		for _, s := range out.Steps {
			if s.Status == StepCompleted || s.Status == StepSkipped {
				done++
			}
			_, c := dashboardCardMarker(s.Status)
			bar = append(bar, c)
		}
		line("%-*s%d of %d done", dashboardCardTextHeaderWidth, "STEPS", done, len(out.Steps))
		line("[%s]", bar)
		g := dashboardCardTextGap
		for _, s := range out.Steps {
			marker, _ := dashboardCardMarker(s.Status)
			// The name is padded, so one space ends it.
			row := fmt.Sprintf("%s%s %-*s ", dashboardCardTextStepIndent, marker, dashboardCardTextNameWidth, s.Name)
			if s.TasksDone != nil && s.TasksTotal != nil {
				line("%s%d/%d tasks%s%s", row, *s.TasksDone, *s.TasksTotal, g, s.Status)
			} else {
				line("%s%s", row, s.Status)
			}
			for _, w := range s.Waves {
				line("%s%s%s%d/%d tasks%s%s", dashboardCardTextWaveIndent, w.Name, g, w.TasksDone, w.TasksTotal, g, w.Status)
			}
		}
	}
	line("")
	issues := "none"
	if len(out.Issues) > 0 {
		parts := make([]string, 0, len(out.Issues))
		for _, k := range slices.Sorted(maps.Keys(out.Issues)) {
			parts = append(parts, fmt.Sprintf("%s %d", k, out.Issues[k]))
		}
		issues = strings.Join(parts, ", ")
	}
	switch server {
	case dashboardCardServerRunning:
		line("ISSUES   %s", issues)
		fmt.Fprintf(&b, "Dashboard: %s", out.DashboardURL)
	case dashboardCardServerUnknown:
		line("ISSUES   %s", issues)
		b.WriteString(dashboardCardTextNoLink)
	default:
		fmt.Fprintf(&b, "ISSUES   %s", issues)
	}
	return b.String()
}

// dashboardCardSteps returns the card steps of pipeline p. A step whose
// detail holds exactly one wave (a wave <n> step of an execute run) gets the
// task counters of that wave. Every other step, such as the queued step of an
// execute run or a ship step, gets no counters.
func dashboardCardSteps(p DashboardPipeline) []DashboardCardStep {
	out := make([]DashboardCardStep, 0, len(p.Steps))
	for _, s := range p.Steps {
		step := DashboardCardStep{Name: s.Name, Status: s.Status}
		if s.Detail != nil && s.Detail.Kind == dashboardKindWaves && len(s.Detail.Waves) == 1 {
			done, total := dashboardCardWaveCounts(s.Detail.Waves[0])
			step.TasksDone, step.TasksTotal = &done, &total
		}
		out = append(out, step)
	}
	return out
}

// dashboardCardWaveCounts returns the count of the tasks of w with status
// completed and the count of all tasks of w. A failed, skipped-dependency or
// pending task is not done, the same as the counter of the dashboard page.
func dashboardCardWaveCounts(w DashboardWave) (done, total int) {
	for _, t := range w.Tasks {
		if t.Status == StepCompleted {
			done++
		}
	}
	return done, len(w.Tasks)
}

// dashboardCardShipWaves returns the waves rows of the ship execute step: the
// wave steps of the execute run that dashboardJoinRuns joins to the ship run
// (pipeline p), the same join as the dashboard page. It returns nil when no
// execute run joins, and an empty list when the joined execute run has no
// wave step yet. all is the state list of the one state.List call of this
// card call. Only the execute runs of the same branch slug as the ship go to
// the join; the slug is state.SlugifyBranch of the pipeline branch, the rule
// of the join. When two ship runs of one branch overlap in time, the page can
// join the execute run to the other ship; the card does not see that ship.
func dashboardCardShipWaves(p DashboardPipeline, all []*state.State) []DashboardCardWave {
	slug := state.SlugifyBranch(p.Branch)
	if slug == "" {
		return nil
	}
	ps := []DashboardPipeline{p}
	execs := map[string]DashboardPipeline{}
	for _, st := range all {
		// dashboardStateBranch is the branch that dashboardStatePipeline
		// stores, so the card builds no pipeline of an unrelated run.
		if st.Prefix != dashboardRunKindExecute || state.SlugifyBranch(dashboardStateBranch(st)) != slug {
			continue
		}
		// The prefix is execute, so ok is always true.
		ep, _, _ := dashboardStatePipeline(st, nil)
		ps = append(ps, ep)
		execs[ep.ID] = ep
	}
	if len(execs) == 0 {
		return nil
	}
	// dashboardJoinRuns keeps the order of its input, so the ship stays at
	// index 0, and it adds the id of the joined execute run to the join
	// members of the ship.
	joined := dashboardJoinRuns(ps)
	for _, id := range joined[0].join.members {
		ep, ok := execs[id]
		if !ok {
			continue
		}
		waves := []DashboardCardWave{}
		for _, s := range dashboardCardSteps(ep) {
			if s.TasksDone == nil || s.TasksTotal == nil {
				continue // the queued step is not a wave
			}
			waves = append(waves, DashboardCardWave{Name: s.Name, Status: s.Status, TasksDone: *s.TasksDone, TasksTotal: *s.TasksTotal})
		}
		return waves
	}
	return nil
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
			OpenWorld:   false, // the only HTTP call is the loopback health probe of the local dashboard server
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
