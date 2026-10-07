package hooks

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/config"
	"github.com/rnagrodzki/sdlc-plugin/internal/dashboard"
	"github.com/rnagrodzki/sdlc-plugin/internal/fsx"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
)

// ---------------------------------------------------------------------------
// Dashboard phase
//
// Every case below isolates both the dashboard cache dir (SDLC_CACHE_DIR)
// and the user config file (config.UserConfigPathEnv), so no test ever
// reads or writes the real developer's ~/.sdlc-cache or ~/.sdlc/local.toml —
// mirroring internal/dashboard's own registry_test.go/settings_test.go
// isolation helpers, duplicated here since they're unexported there. Cases
// that exercise dashboard.Ensure inject a fake dashboard.Deps through
// dashboardDepsFunc, so no real process, network call, or sleep ever runs.
// ---------------------------------------------------------------------------

// isolateDashboardCacheDir points paths.CacheDir() at a throwaway temp
// directory for the test's duration.
func isolateDashboardCacheDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("SDLC_CACHE_DIR", dir)
	return dir
}

// isolateDashboardUserConfig points config.UserConfigPath at a file that
// does not exist, so ReadSettings never reads the real user-level config.
func isolateDashboardUserConfig(t *testing.T) {
	t.Helper()
	t.Setenv(config.UserConfigPathEnv, filepath.Join(t.TempDir(), "unused-user-local.toml"))
}

// writeDashboardSection writes section as root's project-level [dashboard]
// table in .sdlc-v2/local.toml.
func writeDashboardSection(t *testing.T, root string, section map[string]any) {
	t.Helper()
	dir := filepath.Join(root, paths.DataDir)
	mustMkdirAll(t, dir)
	path := filepath.Join(dir, paths.LocalConfigFile)
	if err := fsx.AtomicWriteTOML(path, map[string]any{"dashboard": section}); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// dashboardRootPaths returns the Root field of every entry dashboard.Roots()
// currently considers live.
func dashboardRootPaths(t *testing.T) []string {
	t.Helper()
	roots, err := dashboard.Roots(time.Now())
	if err != nil {
		t.Fatalf("dashboard.Roots: %v", err)
	}
	out := make([]string, len(roots))
	for i, r := range roots {
		out[i] = r.Root
	}
	return out
}

// dashboardFakeDeps is a minimal dashboard.Deps fake for dashboardPhase
// tests: Spawn records a call and immediately makes its port answer
// healthy, simulating a server that starts fast enough to answer the next
// session's probe — mirroring internal/dashboard's own control_test.go
// fakeWorld.spawnAnswers convention, duplicated here since fakeWorld is
// unexported in that package.
type dashboardFakeDeps struct {
	version string
	exeErr  error

	spawns  int
	healthy map[int]dashboard.Health // port -> health once "started"
}

func newDashboardFakeDeps(version string) *dashboardFakeDeps {
	return &dashboardFakeDeps{version: version, healthy: map[int]dashboard.Health{}}
}

func (f *dashboardFakeDeps) deps() dashboard.Deps {
	return dashboard.Deps{
		Spawn: func(exe string, args []string, logPath string) (int, error) {
			f.spawns++
			pid := 4000 + f.spawns
			if port, err := strconv.Atoi(args[len(args)-1]); err == nil {
				f.healthy[port] = dashboard.Health{PID: pid, Version: f.version}
			}
			return pid, nil
		},
		Health: func(port int, _ time.Duration) (dashboard.Health, error) {
			if h, ok := f.healthy[port]; ok {
				return h, nil
			}
			return dashboard.Health{}, errors.New("connection refused")
		},
		Signal: func(int, os.Signal) error { return nil },
		Exe: func() (string, error) {
			if f.exeErr != nil {
				return "", f.exeErr
			}
			return "/usr/local/bin/sdlc", nil
		},
		Sleep: func(time.Duration) {},
	}
}

// withDashboardDeps points dashboardDepsFunc at fake.deps for the test's
// duration.
func withDashboardDeps(t *testing.T, fake *dashboardFakeDeps) {
	t.Helper()
	orig := dashboardDepsFunc
	dashboardDepsFunc = fake.deps
	t.Cleanup(func() { dashboardDepsFunc = orig })
}

// withPluginVersion points PluginVersion at v for the test's duration.
func withPluginVersion(t *testing.T, v string) {
	t.Helper()
	orig := PluginVersion
	PluginVersion = v
	t.Cleanup(func() { PluginVersion = orig })
}

func TestDashboardPhase_RegistersMainRootSilently(t *testing.T) {
	isolateDashboardCacheDir(t)
	isolateDashboardUserConfig(t)
	branch := "feat/dashboard-register"
	root := gitFixture(t, branch)
	mustMkdirAll(t, filepath.Join(root, paths.DataDir))

	if got := dashboardPhase(); got != nil {
		t.Errorf("dashboardPhase() = %q, want nil when autoStart is unset", got)
	}

	roots := dashboardRootPaths(t)
	if len(roots) != 1 || roots[0] != root {
		t.Errorf("dashboard.Roots() = %v, want exactly [%s]", roots, root)
	}
}

// TestDashboardPhase_RegisterFailureIsSilent points the dashboard cache dir
// at a path that is itself a regular file, so RegisterRoot's own
// os.MkdirAll fails (a path component is not a directory). dashboardPhase
// must still print nothing: registration failures have no banner of their
// own to report on.
func TestDashboardPhase_RegisterFailureIsSilent(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	mustWriteFile(t, blocker, "x")
	t.Setenv("SDLC_CACHE_DIR", blocker)
	isolateDashboardUserConfig(t)

	branch := "feat/dashboard-register-fail"
	gitFixture(t, branch)

	if got := dashboardPhase(); got != nil {
		t.Errorf("dashboardPhase() = %q, want nil when RegisterRoot fails", got)
	}
}

// TestDashboardPhase_LinkedWorktreeRegistersMainRootOnly pins the anchor:
// dashboard.Roots() must show the one repo a `git worktree add` linked
// worktree belongs to, keyed by worktree.MainRoot(), never the linked path
// itself.
func TestDashboardPhase_LinkedWorktreeRegistersMainRootOnly(t *testing.T) {
	isolateDashboardCacheDir(t)
	isolateDashboardUserConfig(t)
	branch := "feat/dashboard-linked"
	mainDir := gitFixture(t, branch)
	mustMkdirAll(t, filepath.Join(mainDir, paths.DataDir))

	linkedDir := filepath.Join(t.TempDir(), "linked")
	runGit(t, mainDir, "worktree", "add", "-b", branch+"-linked", linkedDir)
	linkedDir = realPath(t, linkedDir)
	chdir(t, linkedDir)

	if got := dashboardPhase(); got != nil {
		t.Errorf("dashboardPhase() = %q, want nil", got)
	}

	roots := dashboardRootPaths(t)
	if len(roots) != 1 || roots[0] != mainDir {
		t.Errorf("dashboard.Roots() = %v, want exactly [%s] (the main worktree, not %s)", roots, mainDir, linkedDir)
	}
}

func TestDashboardPhase_AutoStartPrintsDefaultPortBanner(t *testing.T) {
	isolateDashboardCacheDir(t)
	isolateDashboardUserConfig(t)
	branch := "feat/dashboard-autostart-default"
	root := gitFixture(t, branch)
	mustMkdirAll(t, filepath.Join(root, paths.DataDir))
	writeDashboardSection(t, root, map[string]any{"autoStart": true})

	withPluginVersion(t, "v-test")
	fake := newDashboardFakeDeps("v-test")
	withDashboardDeps(t, fake)

	start := time.Now()
	got := dashboardPhase()
	if elapsed := time.Since(start); elapsed > 300*time.Millisecond {
		t.Errorf("dashboardPhase() took %s with fake Deps, want under 300ms", elapsed)
	}

	assertLines(t, got, []string{"sdlc dashboard: http://127.0.0.1:7385"})
	if fake.spawns != 1 {
		t.Errorf("spawns = %d, want 1", fake.spawns)
	}
}

func TestDashboardPhase_AutoStartPrintsConfiguredPortBanner(t *testing.T) {
	isolateDashboardCacheDir(t)
	isolateDashboardUserConfig(t)
	branch := "feat/dashboard-autostart-port"
	root := gitFixture(t, branch)
	mustMkdirAll(t, filepath.Join(root, paths.DataDir))
	writeDashboardSection(t, root, map[string]any{"autoStart": true, "port": 7400})

	withPluginVersion(t, "v-test")
	withDashboardDeps(t, newDashboardFakeDeps("v-test"))

	assertLines(t, dashboardPhase(), []string{"sdlc dashboard: http://127.0.0.1:7400"})
}

func TestDashboardPhase_AutoStartFalseProducesNoBanner(t *testing.T) {
	isolateDashboardCacheDir(t)
	isolateDashboardUserConfig(t)
	branch := "feat/dashboard-autostart-false"
	root := gitFixture(t, branch)
	mustMkdirAll(t, filepath.Join(root, paths.DataDir))
	writeDashboardSection(t, root, map[string]any{"autoStart": false})

	fake := newDashboardFakeDeps("v-test")
	withDashboardDeps(t, fake)

	if got := dashboardPhase(); got != nil {
		t.Errorf("dashboardPhase() = %q, want nil when autoStart is false", got)
	}
	if fake.spawns != 0 {
		t.Errorf("spawns = %d, want 0 (Ensure must not be called)", fake.spawns)
	}
}

func TestDashboardPhase_ConfigErrorPrintsNotStarted(t *testing.T) {
	isolateDashboardCacheDir(t)
	isolateDashboardUserConfig(t)
	branch := "feat/dashboard-config-error"
	root := gitFixture(t, branch)
	mustMkdirAll(t, filepath.Join(root, paths.DataDir))
	writeDashboardSection(t, root, map[string]any{"port": 80}) // out of the 1024-65535 range

	got := dashboardPhase()
	if len(got) != 1 || !strings.HasPrefix(got[0], "sdlc dashboard: not started — ") {
		t.Fatalf("dashboardPhase() = %q, want one 'not started' line", got)
	}
	if !strings.Contains(got[0], "port") {
		t.Errorf("line %q should name the config problem", got[0])
	}
}

func TestDashboardPhase_StartErrorPrintsNotStarted(t *testing.T) {
	isolateDashboardCacheDir(t)
	isolateDashboardUserConfig(t)
	branch := "feat/dashboard-start-error"
	root := gitFixture(t, branch)
	mustMkdirAll(t, filepath.Join(root, paths.DataDir))
	writeDashboardSection(t, root, map[string]any{"autoStart": true})

	fake := newDashboardFakeDeps("v-test")
	fake.exeErr = errors.New("no such file")
	withDashboardDeps(t, fake)

	got := dashboardPhase()
	if len(got) != 1 || !strings.HasPrefix(got[0], "sdlc dashboard: not started — ") {
		t.Fatalf("dashboardPhase() = %q, want one 'not started' line", got)
	}
}

// TestSessionStart_DashboardStartErrorStillExitsZero pins that a dashboard
// start failure degrades the same way every other session-start phase
// does: one banner line, never a non-zero exit code or a hook error.
func TestSessionStart_DashboardStartErrorStillExitsZero(t *testing.T) {
	isolateDashboardCacheDir(t)
	isolateDashboardUserConfig(t)
	branch := "feat/dashboard-start-error-session"
	root := gitFixture(t, branch)
	mustMkdirAll(t, filepath.Join(root, paths.DataDir))
	writeDashboardSection(t, root, map[string]any{"autoStart": true})

	fake := newDashboardFakeDeps("v-test")
	fake.exeErr = errors.New("no such file")
	withDashboardDeps(t, fake)

	out, err := sessionStart(HookCtx{}, Event{Source: "startup"})
	if err != nil {
		t.Fatalf("sessionStart returned error: %v", err)
	}
	if out.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", out.ExitCode)
	}
	if !strings.Contains(out.PlainText, "sdlc dashboard: not started — ") {
		t.Errorf("PlainText missing dashboard not-started line:\n%s", out.PlainText)
	}
}

// TestDashboardPhase_SecondRunStartsNoSecondServer simulates a clear or a
// compaction re-running SessionStart: the second dashboardPhase call must
// find the first call's server already answering and start no second one.
func TestDashboardPhase_SecondRunStartsNoSecondServer(t *testing.T) {
	isolateDashboardCacheDir(t)
	isolateDashboardUserConfig(t)
	branch := "feat/dashboard-no-second-server"
	root := gitFixture(t, branch)
	mustMkdirAll(t, filepath.Join(root, paths.DataDir))
	writeDashboardSection(t, root, map[string]any{"autoStart": true})

	withPluginVersion(t, "v-test")
	fake := newDashboardFakeDeps("v-test")
	withDashboardDeps(t, fake)

	first := dashboardPhase()
	second := dashboardPhase()

	assertLines(t, first, []string{"sdlc dashboard: http://127.0.0.1:7385"})
	assertLines(t, second, []string{"sdlc dashboard: http://127.0.0.1:7385"})
	if fake.spawns != 1 {
		t.Errorf("spawns = %d after two session starts, want 1 (no second server)", fake.spawns)
	}
}
