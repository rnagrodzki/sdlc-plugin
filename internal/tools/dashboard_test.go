package tools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
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
	"github.com/rnagrodzki/sdlc-plugin/internal/t3"
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

func TestDashboardRunEnsureOpenTabBrowserErrorNamesTabAddress(t *testing.T) {
	isolateDashboardTest(t)
	root := mkDashboardRoot(t)
	w := newFakeDashWorld()

	orig := dashboardOpenBrowser
	dashboardOpenBrowser = func(url string) error { return errors.New("no display") }
	t.Cleanup(func() { dashboardOpenBrowser = orig })

	out, err := dashboardRun(w.deps(), root, DashboardIn{Action: "ensure", Open: true, Tab: "setup"})
	if err != nil {
		t.Fatalf("dashboardRun: unexpected error from a failing OpenBrowser: %v", err)
	}
	want := fmt.Sprintf("Could not open the browser automatically (no display). Open %s#setup manually.", out.URL)
	if out.Next != want {
		t.Errorf("Next = %q, want %q", out.Next, want)
	}
	if strings.Contains(out.URL, "#") {
		t.Errorf("URL = %q, want it without a tab suffix", out.URL)
	}
}

// ---------------------------------------------------------------------------
// tab input
// ---------------------------------------------------------------------------

func TestDashboardRunEnsureTabOpensTabAddress(t *testing.T) {
	for _, tab := range dashboardTabs {
		t.Run(tab, func(t *testing.T) {
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

			out, err := dashboardRun(w.deps(), root, DashboardIn{Action: "ensure", Open: true, Tab: tab})
			if err != nil {
				t.Fatalf("dashboardRun: %v", err)
			}
			want := out.URL + "#" + tab
			if openedURL != want {
				t.Errorf("OpenBrowser called with %q, want %q", openedURL, want)
			}
			if wantNext := fmt.Sprintf("Opened %s in the default browser.", want); out.Next != wantNext {
				t.Errorf("Next = %q, want %q", out.Next, wantNext)
			}
			if strings.Contains(out.URL, "#") {
				t.Errorf("URL = %q, want it without a tab suffix", out.URL)
			}
		})
	}
}

func TestDashboardRunEnsureTabWithoutOpenNamesTabAddress(t *testing.T) {
	for _, tab := range dashboardTabs {
		t.Run(tab, func(t *testing.T) {
			isolateDashboardTest(t)
			root := mkDashboardRoot(t)
			w := newFakeDashWorld()

			orig := dashboardOpenBrowser
			dashboardOpenBrowser = func(url string) error {
				t.Errorf("OpenBrowser called with %q, want no browser without open", url)
				return nil
			}
			t.Cleanup(func() { dashboardOpenBrowser = orig })

			out, err := dashboardRun(w.deps(), root, DashboardIn{Action: "ensure", Tab: tab})
			if err != nil {
				t.Fatalf("dashboardRun: %v", err)
			}
			if want := fmt.Sprintf("Open %s#%s in a browser to view the dashboard.", out.URL, tab); out.Next != want {
				t.Errorf("Next = %q, want %q", out.Next, want)
			}
			if strings.Contains(out.URL, "#") {
				t.Errorf("URL = %q, want it without a tab suffix", out.URL)
			}
		})
	}
}

func TestDashboardRunEnsureUnknownTab(t *testing.T) {
	isolateDashboardTest(t)
	root := mkDashboardRoot(t)
	w := newFakeDashWorld()

	_, err := dashboardRun(w.deps(), root, DashboardIn{Action: "ensure", Tab: "bogus"})
	if err == nil {
		t.Fatal("dashboardRun: expected an error for an unknown tab, got nil")
	}
	de := domainErrorOf(t, err)
	if want := `dashboard: unknown tab "bogus"; must be one of: pipelines, activity, history, preplans, setup`; de.Msg != want {
		t.Errorf("DomainError.Msg = %q, want %q", de.Msg, want)
	}
	if want := "Pass one of the listed tab names, or leave tab empty."; de.Suggestion != want {
		t.Errorf("DomainError.Suggestion = %q, want %q", de.Suggestion, want)
	}
	if w.spawns != 0 {
		t.Errorf("spawns = %d, want 0 (server must not start on an unknown tab)", w.spawns)
	}
}

// TestDashboardRunTabWithStatusOrStop checks that a tab, known or unknown,
// with action status or stop fails before the action runs: no spawn, no
// signal, and the running server stays up.
func TestDashboardRunTabWithStatusOrStop(t *testing.T) {
	for _, action := range []string{"status", "stop"} {
		for _, tab := range []string{"setup", "bogus"} {
			t.Run(action+"/"+tab, func(t *testing.T) {
				isolateDashboardTest(t)
				root := mkDashboardRoot(t)
				if err := dashboard.WriteServerRecord(dashboard.ServerRecord{PID: 42, Port: 7385, Version: "v1"}); err != nil {
					t.Fatalf("WriteServerRecord: %v", err)
				}
				w := newFakeDashWorld()
				w.servers[7385] = dashboard.Health{PID: 42, Version: "v1"}

				_, err := dashboardRun(w.deps(), root, DashboardIn{Action: action, Tab: tab})
				if err == nil {
					t.Fatal("dashboardRun: expected an error, got nil")
				}
				de := domainErrorOf(t, err)
				if want := fmt.Sprintf("dashboard: tab is for action ensure only, got action %q", action); de.Msg != want {
					t.Errorf("DomainError.Msg = %q, want %q", de.Msg, want)
				}
				if want := `Remove tab, or call dashboard with action="ensure".`; de.Suggestion != want {
					t.Errorf("DomainError.Suggestion = %q, want %q", de.Suggestion, want)
				}
				if w.spawns != 0 || w.signals != 0 {
					t.Errorf("spawns = %d, signals = %d, want 0 and 0", w.spawns, w.signals)
				}
				if _, ok := w.servers[7385]; !ok {
					t.Error("server on port 7385 is gone, want it still running")
				}
			})
		}
	}
}

// TestDashboardRunUnknownActionWithTab checks that an unknown action with a
// tab reports the unknown action, not the tab rule.
func TestDashboardRunUnknownActionWithTab(t *testing.T) {
	isolateDashboardTest(t)
	root := mkDashboardRoot(t)
	w := newFakeDashWorld()

	_, err := dashboardRun(w.deps(), root, DashboardIn{Action: "bogus", Tab: "setup"})
	if err == nil {
		t.Fatal("dashboardRun: expected an error, got nil")
	}
	de := domainErrorOf(t, err)
	if want := `dashboard: unknown action "bogus"; must be one of: ensure, status, stop`; de.Msg != want {
		t.Errorf("DomainError.Msg = %q, want %q", de.Msg, want)
	}
}

// TestDashboardInEnumsMatchLists checks that the enum tags of DashboardIn
// equal dashboardActions and dashboardTabs, and that the tab description
// names every tab, so the schema hints cannot drift from the lists.
func TestDashboardInEnumsMatchLists(t *testing.T) {
	typ := reflect.TypeOf(DashboardIn{})
	enumOf := func(field string) []string {
		f, ok := typ.FieldByName(field)
		if !ok {
			t.Fatalf("DashboardIn has no field %s", field)
		}
		var vals []string
		for _, opt := range strings.Split(f.Tag.Get("jsonschema"), ",") {
			if v, ok := strings.CutPrefix(opt, "enum="); ok {
				vals = append(vals, v)
			}
		}
		return vals
	}
	if got := enumOf("Action"); !reflect.DeepEqual(got, dashboardActions) {
		t.Errorf("DashboardIn.Action enum = %q, dashboardActions = %q", got, dashboardActions)
	}
	if got := enumOf("Tab"); !reflect.DeepEqual(got, dashboardTabs) {
		t.Errorf("DashboardIn.Tab enum = %q, dashboardTabs = %q", got, dashboardTabs)
	}
	f, _ := typ.FieldByName("Tab")
	if want := "one of " + strings.Join(dashboardTabs, ", ") + "."; !strings.Contains(f.Tag.Get("jsonschema_description"), want) {
		t.Errorf("DashboardIn.Tab description = %q, want it to contain %q", f.Tag.Get("jsonschema_description"), want)
	}
}

// TestDashboardRegisteredToolTab calls the registered dashboard tool over an
// in-memory MCP session with a fake dashboard.Deps, so the tab input is
// pinned at the MCP layer: the next text names the tab address, the url does
// not, and a tab with status or an unknown tab is an error result.
func TestDashboardRegisteredToolTab(t *testing.T) {
	isolateDashboardTest(t)
	root := mkDashboardRoot(t)
	writeDashboardLocalTOML(t, root, map[string]any{"dashboard": map[string]any{"port": 7400}})
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(root))
	t.Chdir(root)
	w := newFakeDashWorld()
	origDeps := dashboardDefaultDeps
	dashboardDefaultDeps = w.deps
	t.Cleanup(func() { dashboardDefaultDeps = origDeps })
	origOpen := dashboardOpenBrowser
	dashboardOpenBrowser = func(url string) error {
		t.Errorf("OpenBrowser called with %q, want no browser without open", url)
		return nil
	}
	t.Cleanup(func() { dashboardOpenBrowser = origOpen })

	t.Run("ensure", func(t *testing.T) {
		res, text := callRegisteredTool(t, RegisterDashboardTools, "dashboard", map[string]any{"action": "ensure", "tab": "setup"})
		if res.IsError {
			t.Fatalf("IsError = true, text:\n%s", text)
		}
		const url = "http://127.0.0.1:7400"
		if want := "Open " + url + "#setup in a browser to view the dashboard."; !strings.Contains(text, want) {
			t.Errorf("text does not contain %q:\n%s", want, text)
		}
		if got := strings.Count(text, url+"#setup"); got != 1 {
			t.Errorf("text names %s#setup %d times, want 1 (only the next text, never the url field):\n%s", url, got, text)
		}
	})
	for _, tc := range []struct {
		name string
		args map[string]any
		want string
	}{
		{"status", map[string]any{"action": "status", "tab": "setup"}, `dashboard: tab is for action ensure only, got action "status"`},
		{"unknown tab", map[string]any{"action": "ensure", "tab": "bogus"}, `dashboard: unknown tab "bogus"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res, text := callRegisteredTool(t, RegisterDashboardTools, "dashboard", tc.args)
			if !res.IsError {
				t.Fatalf("IsError = false, want an error result; text:\n%s", text)
			}
			if !strings.Contains(text, tc.want) {
				t.Errorf("text does not contain %q:\n%s", tc.want, text)
			}
		})
	}
}

// dashboardTabNamesRe matches the TAB_NAMES list in view.js.
var dashboardTabNamesRe = regexp.MustCompile(`TAB_NAMES\s*=\s*\[([^\]]*)\]`)

// dashboardTabsFromJS returns the names in the TAB_NAMES list of a view.js
// text, or nil when the text has no TAB_NAMES list.
func dashboardTabsFromJS(js string) []string {
	m := dashboardTabNamesRe.FindStringSubmatch(js)
	if m == nil {
		return nil
	}
	var names []string
	for _, part := range strings.Split(m[1], ",") {
		name := strings.Trim(strings.TrimSpace(part), `'"`)
		if name != "" {
			names = append(names, name)
		}
	}
	return names
}

// TestDashboardTabsMatchPage checks that the tab names of the tool equal
// TAB_NAMES in view.js in value and order.
func TestDashboardTabsMatchPage(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "dashboard", "web", "static", "view.js"))
	if err != nil {
		t.Fatalf("read view.js: %v", err)
	}
	got := dashboardTabsFromJS(string(b))
	if got == nil {
		t.Fatal("view.js: no TAB_NAMES list found")
	}
	if !reflect.DeepEqual(got, dashboardTabs) {
		t.Errorf("view.js TAB_NAMES = %q, dashboardTabs = %q", got, dashboardTabs)
	}
}

// TestDashboardTabsFromJS pins the TAB_NAMES parser of the parity test, so
// that test cannot pass by accident on a quote style or layout change.
func TestDashboardTabsFromJS(t *testing.T) {
	for _, tc := range []struct {
		name string
		js   string
		want []string
	}{
		{"single quotes", `const TAB_NAMES = ['a', 'b'];`, []string{"a", "b"}},
		{"double quotes", `const TAB_NAMES = ["a", "b"];`, []string{"a", "b"}},
		{"trailing comma", `const TAB_NAMES = ['a', 'b',];`, []string{"a", "b"}},
		{"multi-line", "const TAB_NAMES = [\n  'a',\n  'b',\n];", []string{"a", "b"}},
		{"no spaces", `TAB_NAMES=['a','b']`, []string{"a", "b"}},
		{"no list", `const TABS = ['a', 'b'];`, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := dashboardTabsFromJS(tc.js); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("dashboardTabsFromJS(%q) = %q, want %q", tc.js, got, tc.want)
			}
		})
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
	want := "Start, check, or stop the local sdlc dashboard: one web page on this computer that shows the pipelines of every registered repo. Requires: action. Optional: open, tab (ensure only; one of pipelines, activity, history, preplans, setup; it changes only the opened address and the next text, never url). ensure starts the server when it is not active and returns its address. In a T3 Code thread (T3CODE_THREAD_ID set), ensure with open true opens no browser and returns previewUrl for a T3 preview tab. Optional runId (ensure only, not with tab) points the address at the block of one run; only its form is checked, not that the run exists. A bad runId, or runId with tab, status or stop, returns DomainError. status reports it. stop ends it. This call changes no repo file. The page it serves lets a person change files, and it has no undo: archive a finished run (moves its files to .sdlc-v2/run-archive/ and deletes its working files; the moved files stay there), clear a repo's cache (deletes rotated evidence files, reports of runs that no longer exist and old sdlc-* folders under .sdlc-v2/scratch/, and empties the server log), delete a preplan topic file (.sdlc-v2/preplan/<slug>.md; no recovery), delete a deferred item (rewrites .sdlc-v2/history/deferred.json; to recover, add it again with ship_state deferred_add), delete a learning (rewrites .sdlc-v2/learnings/log.md; to recover, add it again with learnings_log append), set the status of a preplan topic file (rewrites its **Status:** line; to recover, set the old status again), delete History rows (rewrites .sdlc-v2/history/runs.jsonl; no recovery), dismiss a wait (deletes one record in .sdlc-v2/evidence/attention/; the session still waits for its answer), save the Setup form (rewrites the changed keys of .sdlc-v2/config.toml, .sdlc-v2/local.toml or the user file ($SDLC_USER_CONFIG when set, else ~/.sdlc/local.toml); a save to the user file also removes each changed key from .sdlc-v2/local.toml; a save of the version section also creates each missing CI script file and lists it in ciFiles; a refused field value changes no file; to recover, set the old value again, set a removed project key again with the target This project, and delete by hand each file listed in ciFiles), and create the first config of a repo (creates .sdlc-v2/config.toml and .sdlc-v2/local.toml from the templates, each only when it is absent, creates .sdlc-v2/runs/, and adds or updates the sdlc block in .sdlc-v2/.gitignore and in the root .gitignore, and renames a leftover .sdlc-v2/local.json to local.json.bak when no local.json.bak exists; to recover, delete each config file listed in created, and rename local.json.bak back to local.json only when the result lists that rename)."

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

// ---------------------------------------------------------------------------
// T3 Code preview address and runId input
// ---------------------------------------------------------------------------

// fakeBrowser replaces dashboardOpenBrowser and records each address it gets.
func fakeBrowser(t *testing.T) *[]string {
	t.Helper()
	var calls []string
	orig := dashboardOpenBrowser
	dashboardOpenBrowser = func(addr string) error {
		calls = append(calls, addr)
		return nil
	}
	t.Cleanup(func() { dashboardOpenBrowser = orig })
	return &calls
}

func wantT3Next(preview string) string {
	return fmt.Sprintf("T3 Code thread detected. Call mcp__t3-code__preview_open with url %s. If that call fails, print %s and continue.", preview, preview)
}

func TestDashboardRunEnsureT3(t *testing.T) {
	const runID = "ship-feat-x-20261116T175538Z"
	tests := []struct {
		name     string
		in       DashboardIn
		thread   string
		wantFrag string // preview suffix after url; "" means no previewUrl
		wantNext string // "" means wantT3Next(preview)
		wantOpen []string
	}{
		{name: "T3 open, no tab", thread: "thr-1", in: DashboardIn{Action: "ensure", Open: true}, wantFrag: "#pipelines?thread=thr-1"},
		{name: "T3 open, tab", thread: "thr-1", in: DashboardIn{Action: "ensure", Open: true, Tab: "setup"}, wantFrag: "#setup?thread=thr-1"},
		{name: "T3 open, runId", thread: "thr-1", in: DashboardIn{Action: "ensure", Open: true, RunID: runID}, wantFrag: "#" + runID + "?thread=thr-1"},
		{name: "T3 no open", thread: "thr-1", in: DashboardIn{Action: "ensure"}, wantNext: "Open %s in a browser to view the dashboard."},
		{name: "T3 no open, runId", thread: "thr-1", in: DashboardIn{Action: "ensure", RunID: runID}, wantNext: "Open %s#" + runID + " in a browser to view the dashboard."},
		{name: "no T3 open", in: DashboardIn{Action: "ensure", Open: true}, wantNext: "Opened %s in the default browser.", wantOpen: []string{"%s"}},
		{name: "no T3 open, runId", in: DashboardIn{Action: "ensure", Open: true, RunID: runID}, wantNext: "Opened %s#" + runID + " in the default browser.", wantOpen: []string{"%s#" + runID}},
		{name: "no T3 no open", in: DashboardIn{Action: "ensure"}, wantNext: "Open %s in a browser to view the dashboard."},
		{name: "no T3 no open, runId", in: DashboardIn{Action: "ensure", RunID: runID}, wantNext: "Open %s#" + runID + " in a browser to view the dashboard."},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			isolateDashboardTest(t)
			t.Setenv(t3.EnvThreadID, tc.thread)
			root := mkDashboardRoot(t)
			w := newFakeDashWorld()
			calls := fakeBrowser(t)

			out, err := dashboardRun(w.deps(), root, tc.in)
			if err != nil {
				t.Fatalf("dashboardRun: %v", err)
			}
			wantPreview := ""
			if tc.wantFrag != "" {
				wantPreview = out.URL + tc.wantFrag
			}
			if out.PreviewURL != wantPreview {
				t.Errorf("PreviewURL = %q, want %q", out.PreviewURL, wantPreview)
			}
			wantNext := wantT3Next(wantPreview)
			if tc.wantNext != "" {
				wantNext = fmt.Sprintf(tc.wantNext, out.URL)
			}
			if out.Next != wantNext {
				t.Errorf("Next = %q, want %q", out.Next, wantNext)
			}
			var wantOpen []string
			for _, o := range tc.wantOpen {
				wantOpen = append(wantOpen, fmt.Sprintf(o, out.URL))
			}
			if !reflect.DeepEqual(*calls, wantOpen) {
				t.Errorf("browser calls = %v, want %v", *calls, wantOpen)
			}
			if strings.Contains(out.URL, "#") {
				t.Errorf("URL = %q, want it without a fragment", out.URL)
			}
		})
	}
}

func TestDashboardRunStatusStopUnchangedInT3(t *testing.T) {
	for _, action := range []string{"status", "stop"} {
		t.Run(action, func(t *testing.T) {
			isolateDashboardTest(t)
			t.Setenv(t3.EnvThreadID, "thr-1")
			root := mkDashboardRoot(t)
			w := newFakeDashWorld()
			calls := fakeBrowser(t)

			out, err := dashboardRun(w.deps(), root, DashboardIn{Action: action, Open: true})
			if err != nil {
				t.Fatalf("dashboardRun: %v", err)
			}
			if out.PreviewURL != "" {
				t.Errorf("PreviewURL = %q, want empty", out.PreviewURL)
			}
			if strings.Contains(out.Next, "T3") {
				t.Errorf("Next = %q, want no T3 text", out.Next)
			}
			if len(*calls) != 0 {
				t.Errorf("browser calls = %v, want none", *calls)
			}
		})
	}
}

// TestDashboardInRunIDTagFollowsMaxRunSlugLen pins the slug limit that the
// runId description states as a literal to t3.MaxRunSlugLen.
func TestDashboardInRunIDTagFollowsMaxRunSlugLen(t *testing.T) {
	f, ok := reflect.TypeOf(DashboardIn{}).FieldByName("RunID")
	if !ok {
		t.Fatal("DashboardIn has no RunID field")
	}
	want := fmt.Sprintf("ship-, execute- or plan-, then 1 to %d characters of A-Z a-z 0-9 . _ -.", t3.MaxRunSlugLen)
	if got := f.Tag.Get("jsonschema_description"); !strings.Contains(got, want) {
		t.Errorf("RunID description = %q, want it to contain %q", got, want)
	}
}

func TestDashboardRunRunIDErrors(t *testing.T) {
	longSlug := func(n int) string { return strings.Repeat("a", n) }
	tests := []struct {
		name       string
		in         DashboardIn
		wantMsg    string
		wantSuggst string
	}{
		{"status", DashboardIn{Action: "status", RunID: "ship-a"}, `dashboard: runId is for action ensure only, got action "status"`, `Remove runId, or call dashboard with action="ensure".`},
		{"stop", DashboardIn{Action: "stop", RunID: "ship-a"}, `dashboard: runId is for action ensure only, got action "stop"`, `Remove runId, or call dashboard with action="ensure".`},
		{"with tab", DashboardIn{Action: "ensure", Tab: "setup", RunID: "ship-a"}, "dashboard: runId and tab cannot be used together, got tab \"setup\"", "Pass runId to open one run, or tab to open a tab."},
		{"no prefix", DashboardIn{Action: "ensure", RunID: "feat-x"}, "dashboard: runId feat-x is not a run id: ship-, execute- or plan-, then 1 to 200 characters of A-Z a-z 0-9 . _ - (got 6 characters after the prefix)", "Pass the state file name of the run without .json, for example ship-feat-x-20261116T175538Z."},
		{"empty slug", DashboardIn{Action: "ensure", RunID: "ship-"}, "dashboard: runId ship- is not a run id: ship-, execute- or plan-, then 1 to 200 characters of A-Z a-z 0-9 . _ - (got 0 characters after the prefix)", "Pass the state file name of the run without .json, for example ship-feat-x-20261116T175538Z."},
		{"bad character", DashboardIn{Action: "ensure", RunID: "plan-a/b"}, "dashboard: runId plan-a/b is not a run id: ship-, execute- or plan-, then 1 to 200 characters of A-Z a-z 0-9 . _ - (got 3 characters after the prefix)", "Pass the state file name of the run without .json, for example ship-feat-x-20261116T175538Z."},
		{"slug too long", DashboardIn{Action: "ensure", RunID: "execute-" + longSlug(t3.MaxRunSlugLen+1)}, "dashboard: runId execute-" + longSlug(201) + " is not a run id: ship-, execute- or plan-, then 1 to 200 characters of A-Z a-z 0-9 . _ - (got 201 characters after the prefix)", "Pass the state file name of the run without .json, for example ship-feat-x-20261116T175538Z."},
		// Error order: the tab check of a known action runs first, then the
		// runId checks, then the action switch.
		{"unknown action with runId and tab", DashboardIn{Action: "bogus", Tab: "setup", RunID: "ship-a"}, "dashboard: runId and tab cannot be used together, got tab \"setup\"", "Pass runId to open one run, or tab to open a tab."},
		{"unknown action with valid runId", DashboardIn{Action: "bogus", RunID: "ship-a"}, `dashboard: unknown action "bogus"; must be one of: ensure, status, stop`, "Pass one of the listed actions."},
		{"status with runId and tab", DashboardIn{Action: "status", Tab: "setup", RunID: "ship-a"}, `dashboard: tab is for action ensure only, got action "status"`, `Remove tab, or call dashboard with action="ensure".`},
		{"stop with runId and tab", DashboardIn{Action: "stop", Tab: "setup", RunID: "ship-a"}, `dashboard: tab is for action ensure only, got action "stop"`, `Remove tab, or call dashboard with action="ensure".`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			isolateDashboardTest(t)
			root := mkDashboardRoot(t)
			w := newFakeDashWorld()
			_, err := dashboardRun(w.deps(), root, tc.in)
			if err == nil {
				t.Fatal("dashboardRun: expected an error, got nil")
			}
			de := domainErrorOf(t, err)
			if de.Msg != tc.wantMsg {
				t.Errorf("DomainError.Msg = %q, want %q", de.Msg, tc.wantMsg)
			}
			if de.Suggestion != tc.wantSuggst {
				t.Errorf("DomainError.Suggestion = %q, want %q", de.Suggestion, tc.wantSuggst)
			}
			if de.Cause != nil {
				t.Errorf("DomainError.Cause = %v, want nil", de.Cause)
			}
			if w.spawns != 0 {
				t.Errorf("spawns = %d, want 0", w.spawns)
			}
		})
	}
}

func TestDashboardRunRunIDValid(t *testing.T) {
	for _, id := range []string{
		"ship-feat-x-20261116T175538Z",
		"execute-a.b_c-D",
		"plan-x",
		"ship-" + strings.Repeat("a", t3.MaxRunSlugLen),
	} {
		t.Run(fmt.Sprintf("%d chars", len(id)), func(t *testing.T) {
			isolateDashboardTest(t)
			root := mkDashboardRoot(t)
			w := newFakeDashWorld()
			out, err := dashboardRun(w.deps(), root, DashboardIn{Action: "ensure", RunID: id})
			if err != nil {
				t.Fatalf("dashboardRun: %v", err)
			}
			if want := fmt.Sprintf("Open %s#%s in a browser to view the dashboard.", out.URL, id); out.Next != want {
				t.Errorf("Next = %q, want %q", out.Next, want)
			}
		})
	}
}

// setupRegisteredDashboard prepares a fake dashboard world for calls of the
// registered dashboard tool. It returns the repo root the tool reports.
func setupRegisteredDashboard(t *testing.T) string {
	t.Helper()
	isolateDashboardTest(t)
	root := mkDashboardRoot(t)
	writeDashboardLocalTOML(t, root, map[string]any{"dashboard": map[string]any{"port": 7400}})
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(root))
	t.Chdir(root)
	w := newFakeDashWorld()
	origDeps := dashboardDefaultDeps
	dashboardDefaultDeps = w.deps
	t.Cleanup(func() { dashboardDefaultDeps = origDeps })
	return root
}

// wantDashboardEnsureText is the whole rendered output of a started ensure on
// the fake world: the next text, the summary and every field. A non-empty
// previewURL adds the previewUrl line; an empty one leaves it out.
func wantDashboardEnsureText(root, next, previewURL string) string {
	const url = "http://127.0.0.1:7400"
	text := "# dashboard \u2014 ok\n\n**Next:** " + next + "\n\n" +
		"## Summary\nDashboard is running at " + url + " (pid 99, version " + version.Plugin + "). Repos: " + root + ".\n\n" +
		"## Fields\n- url: " + url + "\n- running: true\n- started: true\n- pid: 99\n- port: 7400\n- version: " + version.Plugin + "\n- repos:\n  - " + root + "\n"
	if previewURL != "" {
		text += "- previewUrl: " + previewURL + "\n"
	}
	return text
}

// TestDashboardRegisteredToolT3 calls the registered dashboard tool for each
// path of previewUrl and of runId, and asserts the whole rendered output.
func TestDashboardRegisteredToolT3(t *testing.T) {
	const (
		url   = "http://127.0.0.1:7400"
		runID = "ship-feat-x-20261116T175538Z"
	)
	tests := []struct {
		name        string
		thread      string
		args        map[string]any
		wantNext    string
		wantPreview string
		wantOpen    []string
	}{
		{
			name: "T3 open sets previewUrl", thread: "thr-1",
			args:        map[string]any{"action": "ensure", "open": true},
			wantPreview: url + "#pipelines?thread=thr-1",
			wantNext:    wantT3Next(url + "#pipelines?thread=thr-1"),
		},
		{
			name: "T3 open with tab", thread: "thr-1",
			args:        map[string]any{"action": "ensure", "open": true, "tab": "setup"},
			wantPreview: url + "#setup?thread=thr-1",
			wantNext:    wantT3Next(url + "#setup?thread=thr-1"),
		},
		{
			name: "T3 open with runId", thread: "thr-1",
			args:        map[string]any{"action": "ensure", "open": true, "runId": runID},
			wantPreview: url + "#" + runID + "?thread=thr-1",
			wantNext:    wantT3Next(url + "#" + runID + "?thread=thr-1"),
		},
		{
			name: "T3 no open leaves previewUrl out", thread: "thr-1",
			args:     map[string]any{"action": "ensure"},
			wantNext: "Open " + url + " in a browser to view the dashboard.",
		},
		{
			name:     "no T3 open leaves previewUrl out",
			args:     map[string]any{"action": "ensure", "open": true},
			wantNext: "Opened " + url + " in the default browser.",
			wantOpen: []string{url},
		},
		{
			name:     "no T3 open with runId",
			args:     map[string]any{"action": "ensure", "open": true, "runId": runID},
			wantNext: "Opened " + url + "#" + runID + " in the default browser.",
			wantOpen: []string{url + "#" + runID},
		},
		{
			name:     "no T3 no open with runId",
			args:     map[string]any{"action": "ensure", "runId": runID},
			wantNext: "Open " + url + "#" + runID + " in a browser to view the dashboard.",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := setupRegisteredDashboard(t)
			t.Setenv(t3.EnvThreadID, tc.thread)
			calls := fakeBrowser(t)
			res, text := callRegisteredTool(t, RegisterDashboardTools, "dashboard", tc.args)
			if res.IsError {
				t.Fatalf("IsError = true, text:\n%s", text)
			}
			if want := wantDashboardEnsureText(root, tc.wantNext, tc.wantPreview); text != want {
				t.Errorf("text =\n%q\nwant\n%q", text, want)
			}
			if !reflect.DeepEqual(*calls, tc.wantOpen) {
				t.Errorf("browser calls = %v, want %v", *calls, tc.wantOpen)
			}
		})
	}
}

// TestDashboardRegisteredToolRunIDErrors calls the registered dashboard tool
// with each refused runId and asserts the whole rendered error.
func TestDashboardRegisteredToolRunIDErrors(t *testing.T) {
	const (
		ensureOnly = `Remove runId, or call dashboard with action="ensure".`
		fileName   = "Pass the state file name of the run without .json, for example ship-feat-x-20261116T175538Z."
		rule       = "ship-, execute- or plan-, then 1 to 200 characters of A-Z a-z 0-9 . _ - (got "
	)
	tests := []struct {
		name    string
		args    map[string]any
		wantMsg string
		wantFix string
	}{
		{"status", map[string]any{"action": "status", "runId": "ship-a"}, `dashboard: runId is for action ensure only, got action "status"`, ensureOnly},
		{"stop", map[string]any{"action": "stop", "runId": "ship-a"}, `dashboard: runId is for action ensure only, got action "stop"`, ensureOnly},
		{"with tab", map[string]any{"action": "ensure", "tab": "setup", "runId": "ship-a"}, "dashboard: runId and tab cannot be used together, got tab \"setup\"", "Pass runId to open one run, or tab to open a tab."},
		{"bad runId", map[string]any{"action": "ensure", "runId": "plan-a/b"}, "dashboard: runId plan-a/b is not a run id: " + rule + "3 characters after the prefix)", fileName},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			setupRegisteredDashboard(t)
			calls := fakeBrowser(t)
			res, text := callRegisteredTool(t, RegisterDashboardTools, "dashboard", tc.args)
			if !res.IsError {
				t.Fatalf("IsError = false, want an error result; text:\n%s", text)
			}
			want := "# dashboard \u2014 error (domain)\n\n## What happened\n" + tc.wantMsg + "\n\n## Do this\n" + tc.wantFix + "\n"
			if text != want {
				t.Errorf("text =\n%q\nwant\n%q", text, want)
			}
			if len(*calls) != 0 {
				t.Errorf("browser calls = %v, want none", *calls)
			}
		})
	}
}
