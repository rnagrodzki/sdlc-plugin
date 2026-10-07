package tools

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	version "github.com/rnagrodzki/sdlc-plugin"
	"github.com/rnagrodzki/sdlc-plugin/internal/dashboard"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
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
}

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
	switch in.Action {
	case "ensure":
		return dashboardEnsure(d, root, in.Open)
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

// dashboardEnsure resolves [dashboard] settings at root, then ensures a
// server of this version listens on the configured port. A settings error
// stops here — Ensure is never called, so no server starts on a bad config.
func dashboardEnsure(d dashboard.Deps, root string, open bool) (DashboardOut, error) {
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

	next := fmt.Sprintf("Open %s in a browser to view the dashboard.", result.URL)
	if open {
		if openErr := dashboardOpenBrowser(result.URL); openErr != nil {
			// Opening the browser is a convenience; it never fails the tool
			// call itself, but the caller is told it did not happen.
			next = fmt.Sprintf("Could not open the browser automatically (%s). Open %s manually.", openErr, result.URL)
		} else {
			next = fmt.Sprintf("Opened %s in the default browser.", result.URL)
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

// RegisterDashboardTools registers the dashboard tool on the server.
func RegisterDashboardTools(s *mcpserver.Server) {
	mcpserver.Register(s, "dashboard",
		"Start, check, or stop the local sdlc dashboard: one web page on this computer that shows the pipelines of every registered repo. Requires: action. Optional: open (ensure only). ensure starts the server when it is not active and returns its address. status reports it. stop ends it.",
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
