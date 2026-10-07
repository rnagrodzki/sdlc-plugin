package tools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	version "github.com/rnagrodzki/sdlc-plugin"
	"github.com/rnagrodzki/sdlc-plugin/internal/config"
	"github.com/rnagrodzki/sdlc-plugin/internal/dashboard"
	"github.com/rnagrodzki/sdlc-plugin/internal/fsx"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
)

// ---------------------------------------------------------------------------
// Fixtures
// ---------------------------------------------------------------------------

// isolateDashboardTest points the dashboard's cache dir and the user-level
// config file at throwaway locations, so these tests never touch the real
// developer's ~/.sdlc-cache or ~/.sdlc/local.toml.
func isolateDashboardTest(t *testing.T) {
	t.Helper()
	t.Setenv("SDLC_CACHE_DIR", t.TempDir())
	t.Setenv(config.UserConfigPathEnv, filepath.Join(t.TempDir(), "unused-user-local.toml"))
}

// mkDashboardRoot creates a throwaway directory with a .sdlc-v2 subdirectory,
// so it passes dashboard.Roots' "still has a .sdlc-v2 folder" check.
func mkDashboardRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, paths.DataDir), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	return root
}

// writeDashboardLocalTOML writes cfg as root's .sdlc-v2/local.toml.
func writeDashboardLocalTOML(t *testing.T, root string, cfg map[string]any) {
	t.Helper()
	dir := filepath.Join(root, paths.DataDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll %s: %v", dir, err)
	}
	path := filepath.Join(dir, paths.LocalConfigFile)
	if err := fsx.AtomicWriteTOML(path, cfg); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// fakeDashWorld simulates the ports on 127.0.0.1 a dashboard.Deps fake needs:
// an sdlc server per port, ports held by another program, and the process
// Spawn starts. No real process starts and no real port opens. Mirrors
// internal/dashboard/control_test.go's fakeWorld, trimmed to what dashboard.go's
// tests need.
type fakeDashWorld struct {
	servers      map[int]dashboard.Health
	foreign      map[int]bool
	exeErr       error
	spawnErr     error
	spawnPID     int
	spawnAnswers bool
	spawnForeign bool
	// healthErr, when set, is returned by Health for any port with no
	// recorded server and not marked foreign — a plain "nobody is there"
	// error distinct from ErrPortForeign (Task 12's "a Health error" row).
	healthErr error

	spawns  int
	signals int
}

func newFakeDashWorld() *fakeDashWorld {
	return &fakeDashWorld{
		servers:      map[int]dashboard.Health{},
		foreign:      map[int]bool{},
		spawnPID:     99,
		spawnAnswers: true,
	}
}

func (w *fakeDashWorld) deps() dashboard.Deps {
	return dashboard.Deps{
		Spawn: func(exe string, args []string, logPath string) (int, error) {
			w.spawns++
			if w.spawnErr != nil {
				return 0, w.spawnErr
			}
			port, _ := strconv.Atoi(args[len(args)-1])
			switch {
			case w.spawnForeign:
				w.foreign[port] = true
			case w.spawnAnswers:
				w.servers[port] = dashboard.Health{PID: w.spawnPID, Version: version.Plugin}
			}
			return w.spawnPID, nil
		},
		Health: func(port int, timeout time.Duration) (dashboard.Health, error) {
			if w.foreign[port] {
				return dashboard.Health{}, fmt.Errorf("%w: status 404", dashboard.ErrPortForeign)
			}
			if h, ok := w.servers[port]; ok {
				return h, nil
			}
			if w.healthErr != nil {
				return dashboard.Health{}, w.healthErr
			}
			return dashboard.Health{}, errors.New("connection refused")
		},
		Signal: func(pid int, sig os.Signal) error {
			w.signals++
			for port, h := range w.servers {
				if h.PID == pid {
					delete(w.servers, port)
				}
			}
			return nil
		},
		Exe: func() (string, error) {
			if w.exeErr != nil {
				return "", w.exeErr
			}
			return "/usr/local/bin/sdlc", nil
		},
		Sleep: func(time.Duration) {},
	}
}

func domainErrorOf(t *testing.T, err error) *mcpserver.DomainError {
	t.Helper()
	var de *mcpserver.DomainError
	if !errors.As(err, &de) {
		t.Fatalf("error %v (%T) is not a *mcpserver.DomainError", err, err)
	}
	return de
}

// ---------------------------------------------------------------------------
// action="ensure"
// ---------------------------------------------------------------------------

func TestDashboardRunEnsureUsesSettingsPort(t *testing.T) {
	isolateDashboardTest(t)
	root := mkDashboardRoot(t)
	writeDashboardLocalTOML(t, root, map[string]any{"dashboard": map[string]any{"port": 7400}})
	w := newFakeDashWorld()
	w.servers[7400] = dashboard.Health{PID: 123, Version: version.Plugin}

	out, err := dashboardRun(w.deps(), root, DashboardIn{Action: "ensure"})
	if err != nil {
		t.Fatalf("dashboardRun: %v", err)
	}
	if out.URL != "http://127.0.0.1:7400" {
		t.Errorf("URL = %q, want http://127.0.0.1:7400", out.URL)
	}
	if !out.Running {
		t.Error("Running = false, want true")
	}
	if out.Port != 7400 {
		t.Errorf("Port = %d, want 7400", out.Port)
	}
	if out.Next == "" {
		t.Error("Next is empty, want guidance text on a success path")
	}
	if len(out.Repos) != 1 || out.Repos[0] != root {
		t.Errorf("Repos = %v, want [%s]", out.Repos, root)
	}
	if strings.Contains(out.Summary, "(none)") {
		t.Errorf("Summary = %q, want it to list the registered repo, not (none)", out.Summary)
	}
}

func TestDashboardRunEnsureSettingsErrorStopsBeforeStart(t *testing.T) {
	isolateDashboardTest(t)
	root := mkDashboardRoot(t)
	// An out-of-range port is ReadSettings' own *mcpserver.DomainError path.
	writeDashboardLocalTOML(t, root, map[string]any{"dashboard": map[string]any{"port": 80}})
	wantDE := func() *mcpserver.DomainError {
		_, err := dashboard.ReadSettings(root)
		if err == nil {
			t.Fatal("ReadSettings: expected an error for an out-of-range port, got nil")
		}
		return domainErrorOf(t, err)
	}()

	w := newFakeDashWorld()
	_, err := dashboardRun(w.deps(), root, DashboardIn{Action: "ensure"})
	if err == nil {
		t.Fatal("dashboardRun: expected an error, got nil")
	}
	de := domainErrorOf(t, err)
	if de.Msg != wantDE.Msg {
		t.Errorf("DomainError.Msg = %q, want the settings error verbatim: %q", de.Msg, wantDE.Msg)
	}
	if de.Suggestion != wantDE.Suggestion {
		t.Errorf("DomainError.Suggestion = %q, want ReadSettings' own Suggestion preserved: %q", de.Suggestion, wantDE.Suggestion)
	}
	if w.spawns != 0 {
		t.Errorf("spawns = %d, want 0 (server must not start on a settings error)", w.spawns)
	}
}

func TestDashboardRunEnsureSettingsParseErrorStopsBeforeStart(t *testing.T) {
	isolateDashboardTest(t)
	root := mkDashboardRoot(t)
	path := filepath.Join(root, paths.DataDir, paths.LocalConfigFile)
	if err := fsx.AtomicWriteBytes(path, []byte("not = [valid toml")); err != nil {
		t.Fatalf("write malformed local.toml: %v", err)
	}
	wantErr := func() string {
		_, err := dashboard.ReadSettings(root)
		if err == nil {
			t.Fatal("ReadSettings: expected an error for malformed TOML, got nil")
		}
		return err.Error()
	}()

	w := newFakeDashWorld()
	_, err := dashboardRun(w.deps(), root, DashboardIn{Action: "ensure"})
	if err == nil {
		t.Fatal("dashboardRun: expected an error, got nil")
	}
	de := domainErrorOf(t, err)
	if de.Msg != wantErr {
		t.Errorf("DomainError.Msg = %q, want the settings error verbatim: %q", de.Msg, wantErr)
	}
	if w.spawns != 0 {
		t.Errorf("spawns = %d, want 0 (server must not start on a settings error)", w.spawns)
	}
}

func TestDashboardRunEnsureOpenTrueOpensBrowser(t *testing.T) {
	isolateDashboardTest(t)
	root := mkDashboardRoot(t)
	w := newFakeDashWorld()

	var openedURL string
	orig := dashboardOpenBrowser
	dashboardOpenBrowser = func(url string) error {
		openedURL = url
		return nil
	}
	t.Cleanup(func() { dashboardOpenBrowser = orig })

	out, err := dashboardRun(w.deps(), root, DashboardIn{Action: "ensure", Open: true})
	if err != nil {
		t.Fatalf("dashboardRun: %v", err)
	}
	if openedURL != out.URL {
		t.Errorf("OpenBrowser called with %q, want %q", openedURL, out.URL)
	}
	if out.Next == "" {
		t.Error("Next is empty, want guidance text on a success path")
	}
}

func TestDashboardRunEnsureOpenBrowserErrorDoesNotFailCall(t *testing.T) {
	isolateDashboardTest(t)
	root := mkDashboardRoot(t)
	w := newFakeDashWorld()

	orig := dashboardOpenBrowser
	dashboardOpenBrowser = func(url string) error { return errors.New("no display") }
	t.Cleanup(func() { dashboardOpenBrowser = orig })

	out, err := dashboardRun(w.deps(), root, DashboardIn{Action: "ensure", Open: true})
	if err != nil {
		t.Fatalf("dashboardRun: unexpected error from a failing OpenBrowser: %v", err)
	}
	if out.Next == "" {
		t.Error("Next is empty, want it to surface the OpenBrowser failure softly")
	}
}

// Error table rows (Task 5 Contract): ErrPortForeign, ErrSpawn,
// ErrStartTimeout, ErrUnsupported, exercised through dashboardRun(action="ensure").
func TestDashboardRunEnsureErrorTable(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(w *fakeDashWorld)
		wantErr error
	}{
		{
			name:    "port answers but not with sdlc health",
			setup:   func(w *fakeDashWorld) { w.foreign[7385] = true },
			wantErr: dashboard.ErrPortForeign,
		},
		{
			name:    "start of the process fails",
			setup:   func(w *fakeDashWorld) { w.spawnErr = errors.New("exec format error") },
			wantErr: dashboard.ErrSpawn,
		},
		{
			name:    "no health answer after start",
			setup:   func(w *fakeDashWorld) { w.spawnAnswers = false },
			wantErr: dashboard.ErrStartTimeout,
		},
		{
			name:    "unsupported OS",
			setup:   func(w *fakeDashWorld) { w.spawnErr = dashboard.ErrUnsupported },
			wantErr: dashboard.ErrUnsupported,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isolateDashboardTest(t)
			root := mkDashboardRoot(t)
			w := newFakeDashWorld()
			tt.setup(w)

			_, err := dashboardRun(w.deps(), root, DashboardIn{Action: "ensure"})
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("dashboardRun error = %v, want %v", err, tt.wantErr)
			}
			domainErrorOf(t, err)
		})
	}
}

// ---------------------------------------------------------------------------
// action="status" / action="stop"
// ---------------------------------------------------------------------------

func TestDashboardRunStatusNoServer(t *testing.T) {
	isolateDashboardTest(t)
	root := mkDashboardRoot(t)
	w := newFakeDashWorld()

	out, err := dashboardRun(w.deps(), root, DashboardIn{Action: "status"})
	if err != nil {
		t.Fatalf("dashboardRun: %v", err)
	}
	if out.Running {
		t.Error("Running = true, want false (no server record)")
	}
	if out.Next == "" {
		t.Error("Next is empty, want guidance text on a success path")
	}
	if !strings.Contains(out.Summary, "(none)") {
		t.Errorf("Summary = %q, want it to render (none) with no registered repos", out.Summary)
	}
}

// TestDashboardRunStatusHealthError exercises the "a Health error" row: a
// recorded server whose port gives a plain (non-ErrPortForeign) error, the
// ordinary "nobody is listening anymore" case.
func TestDashboardRunStatusHealthError(t *testing.T) {
	isolateDashboardTest(t)
	root := mkDashboardRoot(t)
	if err := dashboard.WriteServerRecord(dashboard.ServerRecord{PID: 11, Port: 7385, Version: "v1"}); err != nil {
		t.Fatalf("WriteServerRecord: %v", err)
	}
	w := newFakeDashWorld()
	w.healthErr = errors.New("connection refused")

	out, err := dashboardRun(w.deps(), root, DashboardIn{Action: "status"})
	if err != nil {
		t.Fatalf("dashboardRun: %v", err)
	}
	if out.Running {
		t.Error("Running = true, want false (Health returned a plain error)")
	}
	if out.Next == "" {
		t.Error("Next is empty, want guidance text on a success path")
	}
}

func TestDashboardRunStop(t *testing.T) {
	isolateDashboardTest(t)
	root := mkDashboardRoot(t)
	if err := dashboard.WriteServerRecord(dashboard.ServerRecord{PID: 42, Port: 7385, Version: "v1"}); err != nil {
		t.Fatalf("WriteServerRecord: %v", err)
	}
	w := newFakeDashWorld()
	w.servers[7385] = dashboard.Health{PID: 42, Version: "v1"}

	out, err := dashboardRun(w.deps(), root, DashboardIn{Action: "stop"})
	if err != nil {
		t.Fatalf("dashboardRun: %v", err)
	}
	if out.Running {
		t.Error("Running = true, want false after a successful stop")
	}
	if w.signals != 1 {
		t.Errorf("signals = %d, want 1 (SIGTERM sent)", w.signals)
	}
	if out.Next == "" {
		t.Error("Next is empty, want guidance text on a success path")
	}
}

func TestDashboardRunStopNoServer(t *testing.T) {
	isolateDashboardTest(t)
	root := mkDashboardRoot(t)
	w := newFakeDashWorld()

	out, err := dashboardRun(w.deps(), root, DashboardIn{Action: "stop"})
	if err != nil {
		t.Fatalf("dashboardRun: %v", err)
	}
	if out.Running {
		t.Error("Running = true, want false (no server record)")
	}
	if !strings.Contains(out.Summary, "(none)") {
		t.Errorf("Summary = %q, want it to render (none) with no registered repos", out.Summary)
	}
}

// ---------------------------------------------------------------------------
// Unknown action
// ---------------------------------------------------------------------------

func TestDashboardRunUnknownAction(t *testing.T) {
	isolateDashboardTest(t)
	root := mkDashboardRoot(t)
	w := newFakeDashWorld()

	_, err := dashboardRun(w.deps(), root, DashboardIn{Action: "bogus"})
	if err == nil {
		t.Fatal("dashboardRun: expected an error for an unknown action, got nil")
	}
	de := domainErrorOf(t, err)
	for _, want := range []string{"ensure", "status", "stop"} {
		if !strings.Contains(de.Msg, want) {
			t.Errorf("DomainError.Msg = %q, want it to list %q", de.Msg, want)
		}
	}
}

// ---------------------------------------------------------------------------
// Root resolution: MainRoot, not ActiveRoot
// ---------------------------------------------------------------------------

// TestDashboardEnsureUsesMainWorktreeRoot runs a real `git worktree add` in a
// t.TempDir() fixture, then calls dashboardRoot() (the same root resolution
// RegisterDashboardTools' closure uses) from inside the linked worktree. It
// must resolve to the MAIN worktree root, and feeding that root through
// dashboardRun(action="ensure") must register exactly one dashboard.Roots
// entry naming the main root — never the linked worktree's own path.
func TestDashboardEnsureUsesMainWorktreeRoot(t *testing.T) {
	isolateDashboardTest(t)

	mainRoot := t.TempDir()
	initGitFixture(t, mainRoot)
	writeFile(t, filepath.Join(mainRoot, paths.DataDir, ".keep"), "")
	writeFile(t, filepath.Join(mainRoot, "README.md"), "root\n")
	runGit(t, mainRoot, "add", "-A")
	runGit(t, mainRoot, "commit", "-m", "init")

	linkedRoot := filepath.Join(t.TempDir(), "linked")
	runGit(t, mainRoot, "worktree", "add", "-b", "feature-x", linkedRoot)

	origWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(origWD) })
	if err := os.Chdir(linkedRoot); err != nil {
		t.Fatalf("Chdir: %v", err)
	}

	resolvedRoot, err := dashboardRoot()
	if err != nil {
		t.Fatalf("dashboardRoot: %v", err)
	}
	wantMain, err := filepath.EvalSymlinks(mainRoot)
	if err != nil {
		t.Fatalf("EvalSymlinks(mainRoot): %v", err)
	}
	gotMain, err := filepath.EvalSymlinks(resolvedRoot)
	if err != nil {
		t.Fatalf("EvalSymlinks(resolvedRoot): %v", err)
	}
	if gotMain != wantMain {
		t.Fatalf("dashboardRoot() = %s, want the main worktree root %s", resolvedRoot, mainRoot)
	}

	w := newFakeDashWorld()
	if _, err := dashboardRun(w.deps(), resolvedRoot, DashboardIn{Action: "ensure"}); err != nil {
		t.Fatalf("dashboardRun: %v", err)
	}

	roots, err := dashboard.Roots(time.Now())
	if err != nil {
		t.Fatalf("dashboard.Roots: %v", err)
	}
	if len(roots) != 1 {
		t.Fatalf("dashboard.Roots() = %v, want exactly one registered root", roots)
	}
	gotRegistered, err := filepath.EvalSymlinks(roots[0].Root)
	if err != nil {
		t.Fatalf("EvalSymlinks(roots[0].Root): %v", err)
	}
	if gotRegistered != wantMain {
		t.Errorf("registered root = %s, want the main worktree root %s (not the linked worktree)", roots[0].Root, mainRoot)
	}
}

// ---------------------------------------------------------------------------
// Tool description
// ---------------------------------------------------------------------------

func TestDashboardToolDescriptionExact(t *testing.T) {
	want := "Start, check, or stop the local sdlc dashboard: one web page on this computer that shows the pipelines of every registered repo. Requires: action. Optional: open (ensure only). ensure starts the server when it is not active and returns its address. status reports it. stop ends it."

	s := mcpserver.New("test", "0.0.0-test")
	RegisterDashboardTools(s)

	ctx := context.Background()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	if _, err := s.MCPServer().Connect(ctx, serverTransport, nil); err != nil {
		t.Fatalf("server Connect: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0.0.0"}, nil)
	c, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client Connect: %v", err)
	}
	t.Cleanup(func() { c.Close() })

	resp, err := c.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	for _, tool := range resp.Tools {
		if tool.Name == "dashboard" {
			if tool.Description != want {
				t.Errorf("Description = %q, want %q", tool.Description, want)
			}
			return
		}
	}
	t.Fatal(`ListTools: no "dashboard" tool registered`)
}
