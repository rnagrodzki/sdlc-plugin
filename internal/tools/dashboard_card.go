package tools

import (
	_ "embed"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
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
	Name       string `json:"name" jsonschema_description:"wave <n>."`
	Status     string `json:"status" jsonschema_description:"Wave status, the same as the wave <n> step of the execute run card."`
	TasksDone  int    `json:"tasksDone" jsonschema_description:"Count of the tasks of the wave with status completed."`
	TasksTotal int    `json:"tasksTotal" jsonschema_description:"Count of all tasks of the wave."`
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
	Snapshot     bool                `json:"snapshot,omitempty" jsonschema_description:"true for a middle card that the view draws once and does not refresh. false when final is true, because final wins."`
	T3ThreadID   string              `json:"t3ThreadId,omitempty" jsonschema_description:"T3 Code thread id stored in the run state. Absent when the run has none."`
	StartedAt    string              `json:"startedAt" jsonschema_description:"Start time of the run, RFC 3339."`
	CompletedAt  *string             `json:"completedAt" jsonschema_description:"End time of a completed run, RFC 3339. null while the run has no end time; the text renders it as (none)."`
	Steps        []DashboardCardStep `json:"steps" jsonschema_description:"Steps of the run in order. Empty when the run has no step yet."`
	Issues       map[string]int      `json:"issues" jsonschema_description:"Count of the issues of the run for each severity. Empty when the run has no issue."`
	Next         string              `json:"next" jsonschema_description:"What the caller does next."`
	Text         string              `json:"text,omitempty" jsonschema_description:"Client with no card only: the run card as a fixed-width text block. Print it in the chat unchanged, in a code block."`
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
const dashboardCardDescription = "Show one ship or execute run as an MCP App card (view ui://sdlc/dashboard-card) in a client that supports MCP Apps. Other clients get the card as a text block, plus the dashboard link of the run when the dashboard runs. Requires: runId. Optional: final (end card), snapshot (middle card, no refresh). Reads the run state files in <main-worktree>/.sdlc-v2/runs and the dashboard server record, and writes nothing. Call it at each card point of a top-level ship run or a standalone execute run. Errors: DomainError for a bad runId, a missing run, or a run of another T3 thread. DataError when the run is not found and some run files could not be read, or, in a T3 Code thread, when the run stores a t3ThreadId that is not a string. InfraError when the runs folder cannot be listed. A dashboard server that cannot be read is not an error: the card says that the dashboard is not running."

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

// dashboardCardNoView is the Next text for a client that shows no MCP App
// view, when the dashboard server runs. The ship and execute skills match its
// first sentence.
const dashboardCardNoView = "This client shows no card. Print the text field in the chat unchanged, in a code block. Call dashboard_card again at the next card point of this run."

// dashboardCardNoViewNoServer is the Next text for a client that shows no MCP
// App view, when no dashboard server runs. Its two %s verbs are the run id. The
// first sentence is the same as dashboardCardNoView.
const dashboardCardNoViewNoServer = "This client shows no card. Print the text field in the chat unchanged, in a code block. If no dashboard ensure ran in this run, call dashboard({action:\"ensure\", runId:\"%s\"}) and print its link with #%s. If ensure fails, print one warning line and continue with no link. Call dashboard_card again at the next card point of this run."

// dashboardCardServerURL returns the address of the running dashboard server
// and true, or "" and false when no server runs or its record cannot be read.
// It is a package-level seam, like dashboardDefaultDeps, so tests never reach
// a real server.
var dashboardCardServerURL = func() (string, bool) {
	return dashboardCardServerFrom(dashboard.Status(dashboardDefaultDeps()))
}

// dashboardCardServerFrom maps the result of dashboard.Status to the address
// of a running server. A card error must never stop a run, so an error is
// dropped: the card then says that the dashboard is not running.
func dashboardCardServerFrom(res dashboard.Result, err error) (string, bool) {
	if err != nil || !res.Running || res.URL == "" {
		return "", false
	}
	return res.URL, true
}

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
	if p.Kind == "ship" {
		if waves := dashboardCardShipWaves(st, p, all); waves != nil {
			for i := range out.Steps {
				if out.Steps[i].Name == dashboardShipStepExecute {
					out.Steps[i].Waves = waves
					break
				}
			}
		}
	}
	if showsView {
		out.Next = fmt.Sprintf("Card shows run %s (%s). Continue the current step.", in.RunID, p.Status)
		return out, nil
	}
	// A client with no card gets the card as text, with the link to the run
	// when the dashboard runs.
	if url, ok := dashboardCardServerURL(); ok {
		out.DashboardURL = url + "#" + in.RunID
		out.Next = dashboardCardNoView
	} else {
		out.Next = fmt.Sprintf(dashboardCardNoViewNoServer, in.RunID, in.RunID)
	}
	out.Text = dashboardCardText(out)
	return out, nil
}

// dashboardCardTextNameWidth is the width that a step name is padded to in the
// text block. A longer name is not cut.
const dashboardCardTextNameWidth = 18

// dashboardCardTextNotRecorded stands for an empty branch or time.
const dashboardCardTextNotRecorded = "not recorded"

// dashboardCardMarker returns the marker and the bar character of a step
// status.
func dashboardCardMarker(status string) (marker string, bar byte) {
	switch status {
	case "completed":
		return "[x]", '#'
	case "in_progress":
		return "[>]", '>'
	case "skipped":
		return "[-]", '#'
	case "failed":
		return "[!]", '!'
	}
	return "[ ]", '.'
}

// dashboardCardText renders out as the fixed-width text block for a client
// with no card. The last line is the dashboard link of out.DashboardURL, or
// "Dashboard: not running". The block has no trailing newline.
func dashboardCardText(out DashboardCardOut) string {
	orNotRecorded := func(v string) string {
		if v == "" {
			return dashboardCardTextNotRecorded
		}
		return v
	}
	state := out.Status
	if out.Final && out.Status == "running" {
		state = "stopped"
	}
	completed := ""
	if out.CompletedAt != nil {
		completed = *out.CompletedAt
	}
	var b strings.Builder
	line := func(format string, args ...any) { fmt.Fprintf(&b, format+"\n", args...) }
	line("%s  [%s]", strings.ToUpper(out.Kind), state)
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
			if s.Status == "completed" || s.Status == "skipped" {
				done++
			}
			_, c := dashboardCardMarker(s.Status)
			bar = append(bar, c)
		}
		line("%-40s%d of %d done", "STEPS", done, len(out.Steps))
		line("[%s]", bar)
		for _, s := range out.Steps {
			marker, _ := dashboardCardMarker(s.Status)
			if s.TasksDone != nil && s.TasksTotal != nil {
				line("  %s %-*s %d/%d tasks  %s", marker, dashboardCardTextNameWidth, s.Name, *s.TasksDone, *s.TasksTotal, s.Status)
			} else {
				line("  %s %-*s %s", marker, dashboardCardTextNameWidth, s.Name, s.Status)
			}
			for _, w := range s.Waves {
				line("        %s   %d/%d tasks   %s", w.Name, w.TasksDone, w.TasksTotal, w.Status)
			}
		}
	}
	line("")
	issues := "none"
	if len(out.Issues) > 0 {
		keys := make([]string, 0, len(out.Issues))
		for k := range out.Issues {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			parts = append(parts, fmt.Sprintf("%s %d", k, out.Issues[k]))
		}
		issues = strings.Join(parts, ", ")
	}
	line("ISSUES   %s", issues)
	if out.DashboardURL != "" {
		fmt.Fprintf(&b, "Dashboard: %s", out.DashboardURL)
	} else {
		b.WriteString("Dashboard: not running")
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
// ship (pipeline p), the same join as the dashboard page. It returns nil when
// no execute run joins. all is the state list of the one state.List call of
// this card call. Only the execute runs of the same branch slug as the ship
// go to the join, so the card builds no pipeline of an unrelated run. When
// two ship runs of one branch overlap in time, the page can join the execute
// run to the other ship; the card does not see that ship.
func dashboardCardShipWaves(ship *state.State, p DashboardPipeline, all []*state.State) []DashboardCardWave {
	slug := dashboardCardBranchSlug(ship)
	if slug == "" {
		return nil
	}
	ps := []DashboardPipeline{p}
	execs := map[string]DashboardPipeline{}
	for _, st := range all {
		if st.Prefix != "execute" || dashboardCardBranchSlug(st) != slug {
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
			if s.TasksDone == nil {
				continue // the queued step is not a wave
			}
			waves = append(waves, DashboardCardWave{Name: s.Name, Status: s.Status, TasksDone: *s.TasksDone, TasksTotal: *s.TasksTotal})
		}
		return waves
	}
	return nil
}

// dashboardCardBranchSlug returns the branch slug of st with the rule of the
// join: the slug of the branch key, else the branch slug of the file name.
func dashboardCardBranchSlug(st *state.State) string {
	if b := dashboardStr(st.Data["branch"]); b != "" {
		return state.SlugifyBranch(b)
	}
	return st.BranchSlug
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
