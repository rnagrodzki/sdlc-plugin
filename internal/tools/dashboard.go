package tools

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	version "github.com/rnagrodzki/sdlc-plugin"
	"github.com/rnagrodzki/sdlc-plugin/internal/config"
	"github.com/rnagrodzki/sdlc-plugin/internal/dashboard"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
	"github.com/rnagrodzki/sdlc-plugin/internal/worktree"
)

// dashboardOpenBrowser is dashboard.OpenBrowser as a package-level seam, so
// tests can replace it with a fake that never spawns a real OS process
// (dashboard.OpenBrowser execs "open"/"xdg-open" with the server's URL).
// Production code uses dashboard.OpenBrowser unchanged.
var dashboardOpenBrowser = dashboard.OpenBrowser

// ---------------------------------------------------------------------------
// Input / Output
// ---------------------------------------------------------------------------

// DashboardIn is the dashboard tool's input.
type DashboardIn struct {
	Action string `json:"action" jsonschema:"enum=ensure,enum=status,enum=stop" jsonschema_description:"Plain text, one of ensure, status, stop. ensure: start the dashboard server if it is not active (or start it again after a version or port change) and return its address. status: report it. stop: end it. Example: ensure"`
	Open   bool   `json:"open,omitempty" jsonschema_description:"Plain JSON bool, for action ensure only. true opens the address in the default browser. Example: true"`
	Tab    string `json:"tab,omitempty" jsonschema_description:"Plain text, for action ensure only, one of pipelines, activity, history, preplans, setup. Names the tab of the page. With open true the browser opens that tab. Without open the next field names the address of that tab. The url field never changes. Example: setup"`
}

// dashboardTabs lists the tab names the dashboard page accepts after the "#"
// of its address, in page order. It must equal TAB_NAMES in
// internal/dashboard/web/static/view.js (TestDashboardTabsMatchPage).
var dashboardTabs = []string{"pipelines", "activity", "history", "preplans", "setup"}

// DashboardOut is the dashboard tool's output.
type DashboardOut struct {
	Summary string   `json:"summary"`
	URL     string   `json:"url"`
	Running bool     `json:"running"`
	Started bool     `json:"started"`
	PID     int      `json:"pid"`
	Port    int      `json:"port"`
	Version string   `json:"version"`
	Repos   []string `json:"repos"`
	Next    string   `json:"next"`
}

// ---------------------------------------------------------------------------
// Handler
// ---------------------------------------------------------------------------

// dashboardRoot resolves the repo root dashboard actions apply to: always the
// MAIN git worktree root (worktree.MainRoot()), never worktree.ActiveRoot() —
// a dashboard server registered from a linked worktree must still anchor to
// the same root every other worktree of the same repo resolves to. Falls
// back to the current working directory when git worktree resolution fails,
// matching RegisterLearningsTools' fallback shape.
func dashboardRoot() (string, error) {
	root, err := worktree.MainRoot()
	if err != nil {
		root, err = os.Getwd()
		if err != nil {
			return "", &mcpserver.InfraError{
				Msg:        fmt.Sprintf("resolve project root: %s", err.Error()),
				Suggestion: "Run dashboard from a directory this process can access; both git-worktree resolution and the current working directory lookup failed.",
				Cause:      err,
			}
		}
	}
	return root, nil
}

// dashboardRun implements every dashboard action against an explicit
// dashboard.Deps, so tests exercise each action and each control.go error row
// through a fake Deps without starting a real process. The Register closure
// below passes dashboard.DefaultDeps() in production.
func dashboardRun(d dashboard.Deps, root string, in DashboardIn) (DashboardOut, error) {
	if in.Tab != "" && in.Action != "ensure" && dashboardKnownAction(in.Action) {
		return DashboardOut{}, &mcpserver.DomainError{
			Msg:        "dashboard: tab is for action ensure only",
			Suggestion: `Remove tab, or call dashboard with action="ensure".`,
		}
	}
	switch in.Action {
	case "ensure":
		if in.Tab != "" && !dashboardTabKnown(in.Tab) {
			return DashboardOut{}, &mcpserver.DomainError{
				Msg:        fmt.Sprintf("dashboard: unknown tab %q; must be one of: %s", in.Tab, strings.Join(dashboardTabs, ", ")),
				Suggestion: "Pass one of the listed tab names, or leave tab empty.",
			}
		}
		return dashboardEnsure(d, root, in.Open, in.Tab)
	case "status":
		result, err := dashboard.Status(d)
		if err != nil {
			return DashboardOut{}, err
		}
		return dashboardOut(result, dashboardStatusNext(result))
	case "stop":
		result, err := dashboard.Stop(d)
		if err != nil {
			return DashboardOut{}, err
		}
		return dashboardOut(result, dashboardStopNext(result))
	default:
		return DashboardOut{}, &mcpserver.DomainError{
			Msg: fmt.Sprintf("dashboard: unknown action %q; must be one of: ensure, status, stop", in.Action),
		}
	}
}

// dashboardKnownAction reports whether action is one dashboardRun handles.
func dashboardKnownAction(action string) bool {
	return action == "ensure" || action == "status" || action == "stop"
}

// dashboardTabKnown reports whether tab is a name the dashboard page accepts.
func dashboardTabKnown(tab string) bool {
	for _, t := range dashboardTabs {
		if t == tab {
			return true
		}
	}
	return false
}

// dashboardEnsure resolves [dashboard] settings at root, then ensures a
// server of this version listens on the configured port. A settings error
// stops here — Ensure is never called, so no server starts on a bad config.
// A non-empty tab (already validated) selects the page tab through the "#"
// suffix of the address that is opened or named in Next; the result URL
// never carries the suffix.
func dashboardEnsure(d dashboard.Deps, root string, open bool, tab string) (DashboardOut, error) {
	settings, err := dashboard.ReadSettings(root)
	if err != nil {
		// ReadSettings' own errors already satisfy "carries the settings
		// error text verbatim" either way; a *mcpserver.DomainError (e.g. an
		// out-of-range port) also carries a specific Suggestion that a
		// re-wrap would silently drop (errors.As matches the outer wrapper
		// first), so pass that case through unchanged and only wrap a plain
		// error (e.g. a TOML parse failure) into a DomainError.
		var de *mcpserver.DomainError
		if errors.As(err, &de) {
			return DashboardOut{}, err
		}
		return DashboardOut{}, &mcpserver.DomainError{
			Msg:   err.Error(),
			Cause: err,
		}
	}

	result, err := dashboard.Ensure(d, dashboard.EnsureOpts{
		Root:    root,
		Port:    settings.Port,
		Version: version.Plugin,
		Wait:    true,
	})
	if err != nil {
		return DashboardOut{}, err
	}

	target := result.URL
	if tab != "" {
		target += "#" + tab
	}
	next := fmt.Sprintf("Open %s in a browser to view the dashboard.", target)
	if open {
		if openErr := dashboardOpenBrowser(target); openErr != nil {
			// Opening the browser is a convenience; it never fails the tool
			// call itself, but the caller is told it did not happen.
			next = fmt.Sprintf("Could not open the browser automatically (%s). Open %s manually.", openErr, target)
		} else {
			next = fmt.Sprintf("Opened %s in the default browser.", target)
		}
	}
	return dashboardOut(result, next)
}

// dashboardStatusNext returns the Next guidance for action="status".
func dashboardStatusNext(result dashboard.Result) string {
	if result.Running {
		return fmt.Sprintf("Dashboard is reachable at %s.", result.URL)
	}
	return `Call dashboard with action="ensure" to start the server.`
}

// dashboardStopNext returns the Next guidance for action="stop".
func dashboardStopNext(result dashboard.Result) string {
	if result.Running {
		return "The server did not stop in time; check its log, then try again."
	}
	return "The server is stopped."
}

// dashboardOut maps a dashboard.Result onto DashboardOut, filling Repos (and
// the "(none)" Summary phrasing) from dashboard.Roots independently of which
// action ran.
func dashboardOut(result dashboard.Result, next string) (DashboardOut, error) {
	roots, err := dashboard.Roots(time.Now())
	if err != nil {
		return DashboardOut{}, &mcpserver.InfraError{
			Msg: fmt.Sprintf("dashboard: list registered repos: %s", err.Error()),
		}
	}
	repos := make([]string, 0, len(roots))
	for _, r := range roots {
		repos = append(repos, r.Root)
	}

	return DashboardOut{
		Summary: dashboardSummary(result, repos),
		URL:     result.URL,
		Running: result.Running,
		Started: result.Started,
		PID:     result.PID,
		Port:    result.Port,
		Version: result.Version,
		Repos:   repos,
		Next:    next,
	}, nil
}

// dashboardSummary renders the human-readable status line, with "(none)"
// when repos is empty.
func dashboardSummary(result dashboard.Result, repos []string) string {
	var b strings.Builder
	switch {
	case result.Running:
		fmt.Fprintf(&b, "Dashboard is running at %s (pid %d, version %s).", result.URL, result.PID, result.Version)
	case result.URL != "":
		fmt.Fprintf(&b, "Dashboard is not running (last known address %s).", result.URL)
	default:
		b.WriteString("Dashboard is not running.")
	}
	b.WriteString(" Repos: ")
	if len(repos) == 0 {
		b.WriteString("(none).")
	} else {
		b.WriteString(strings.Join(repos, ", "))
		b.WriteString(".")
	}
	return b.String()
}

// ---------------------------------------------------------------------------
// Registration
// ---------------------------------------------------------------------------

// dashboardToolDescription is the description of the dashboard tool. The
// paths of the data dir and the user file are built from their constants.
var dashboardToolDescription = "Start, check, or stop the local sdlc dashboard: one web page on this computer that shows the pipelines of every registered repo. Requires: action. Optional: open, tab (ensure only). ensure starts the server when it is not active and returns its address. status reports it. stop ends it. This call changes no repo file. The page it serves lets a person change files, and it has no undo: " +
	"archive a finished run (moves its files to " + paths.DataDir + "/run-archive/ and deletes its working files; the moved files stay there), " +
	"clear a repo's cache (deletes rotated evidence files, reports of runs that no longer exist and old sdlc-* temp dirs, and empties the server log), " +
	"delete a preplan topic file (" + paths.DataDir + "/preplan/<slug>.md; no recovery), " +
	"delete a deferred item (rewrites " + paths.DataDir + "/history/deferred.json; to recover, add it again with ship_state deferred_add), " +
	"delete a learning (rewrites " + paths.DataDir + "/learnings/log.md; to recover, add it again with learnings_log append), " +
	"set the status of a preplan topic file (rewrites its **Status:** line; to recover, set the old status again), " +
	"delete History rows (rewrites " + paths.DataDir + "/history/runs.jsonl; no recovery), " +
	"dismiss a wait (deletes one record in " + paths.DataDir + "/evidence/attention/; the session still waits for its answer), " +
	"save the Setup form (rewrites the changed keys of " + paths.DataDir + "/" + paths.ConfigFile + ", " + paths.DataDir + "/" + paths.LocalConfigFile + " or the user file ($" + config.UserConfigPathEnv + " when set, else ~/.sdlc/local.toml); a save to the user file also removes each changed key from " + paths.DataDir + "/" + paths.LocalConfigFile + "; a save of the version section also creates each missing CI script file and lists it in ciFiles; a refused field value changes no file; to recover, set the old value again, set a removed project key again with the target This project, and delete by hand each file listed in ciFiles), " +
	"and create the first config of a repo (creates " + paths.DataDir + "/" + paths.ConfigFile + " and " + paths.DataDir + "/" + paths.LocalConfigFile + " from the templates, each only when it is absent, creates " + paths.DataDir + "/runs/, and adds or updates the sdlc block in " + paths.DataDir + "/.gitignore and in the root .gitignore, and renames a leftover " + paths.DataDir + "/local.json to local.json.bak when no local.json.bak exists; to recover, delete each config file listed in created, and rename local.json.bak back to local.json only when the result lists that rename)."

// RegisterDashboardTools registers the dashboard tool on the server.
func RegisterDashboardTools(s *mcpserver.Server) {
	mcpserver.Register(s, "dashboard",
		dashboardToolDescription,
		mcpserver.Annotations{
			Title:       "Start, check, or stop the local dashboard",
			ReadOnly:    false,
			Destructive: false,
			Idempotent:  true,
			OpenWorld:   false,
		},
		func(ctx mcpserver.Ctx, in DashboardIn) (DashboardOut, error) {
			root, err := dashboardRoot()
			if err != nil {
				return DashboardOut{}, err
			}
			return dashboardRun(dashboard.DefaultDeps(), root, in)
		},
	)
}
