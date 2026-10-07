package dashboard

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
)

// fakeWorld simulates the ports on 127.0.0.1 for Deps fakes: an sdlc server
// per port, ports held by another program, and the processes Spawn starts.
// No real process starts and no real port opens.
type fakeWorld struct {
	servers  map[int]Health // port -> sdlc server answering there
	foreign  map[int]bool   // port -> another program answers there
	exeErr   error
	spawnErr error
	spawnPID int
	// spawnAnswers makes a spawned server answer health on its --port.
	spawnAnswers bool
	// spawnForeign makes the port answer as a foreign program after Spawn.
	spawnForeign bool
	// ignoreSignal keeps a signalled server answering (it does not stop).
	ignoreSignal bool

	healthCalls []int
	spawns      []spawnCall
	signals     []signalCall
	sleeps      int
}

type spawnCall struct {
	exe     string
	args    []string
	logPath string
}

type signalCall struct {
	pid int
	sig os.Signal
}

func newFakeWorld() *fakeWorld {
	return &fakeWorld{
		servers:      map[int]Health{},
		foreign:      map[int]bool{},
		spawnPID:     99,
		spawnAnswers: true,
	}
}

func (w *fakeWorld) deps() Deps {
	return Deps{
		Spawn: func(exe string, args []string, logPath string) (int, error) {
			w.spawns = append(w.spawns, spawnCall{exe, args, logPath})
			if w.spawnErr != nil {
				return 0, w.spawnErr
			}
			port, _ := strconv.Atoi(args[len(args)-1])
			switch {
			case w.spawnForeign:
				w.foreign[port] = true
			case w.spawnAnswers:
				w.servers[port] = Health{PID: w.spawnPID, Version: "v-new"}
			}
			return w.spawnPID, nil
		},
		Health: func(port int, timeout time.Duration) (Health, error) {
			w.healthCalls = append(w.healthCalls, port)
			if w.foreign[port] {
				return Health{}, fmt.Errorf("%w: status 404", ErrPortForeign)
			}
			if h, ok := w.servers[port]; ok {
				return h, nil
			}
			return Health{}, errors.New("connection refused")
		},
		Signal: func(pid int, sig os.Signal) error {
			w.signals = append(w.signals, signalCall{pid, sig})
			if w.ignoreSignal {
				return nil
			}
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
		Sleep: func(time.Duration) { w.sleeps++ },
	}
}

func writeRecord(t *testing.T, r ServerRecord) {
	t.Helper()
	if err := WriteServerRecord(r); err != nil {
		t.Fatalf("WriteServerRecord: %v", err)
	}
}

func suggestionOf(t *testing.T, err error) string {
	t.Helper()
	var de *mcpserver.DomainError
	if !errors.As(err, &de) {
		t.Fatalf("error %v is not a *mcpserver.DomainError", err)
	}
	return de.Suggestion
}

func TestEnsureSameVersionAndPortNotStarted(t *testing.T) {
	isolateCacheDir(t)
	root := mkRepoRoot(t)
	w := newFakeWorld()
	w.servers[7385] = Health{PID: 42, Version: "v1"}
	writeRecord(t, ServerRecord{PID: 42, Port: 7385, Version: "v1"})

	res, err := Ensure(w.deps(), EnsureOpts{Root: root, Port: 7385, Version: "v1", Wait: true})
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	want := Result{URL: "http://127.0.0.1:7385", Port: 7385, PID: 42, Running: true, Version: "v1"}
	if res != want {
		t.Errorf("Ensure = %+v, want %+v", res, want)
	}
	if len(w.spawns) != 0 || len(w.signals) != 0 {
		t.Errorf("spawns=%v signals=%v, want none", w.spawns, w.signals)
	}
	roots, err := Roots(time.Now())
	if err != nil || len(roots) != 1 || roots[0].Root != root {
		t.Errorf("Roots = %v, %v; want one root %s", roots, err, root)
	}
}

func TestEnsureVersionDiffersRestarts(t *testing.T) {
	isolateCacheDir(t)
	w := newFakeWorld()
	w.servers[7385] = Health{PID: 42, Version: "v-old"}
	writeRecord(t, ServerRecord{PID: 42, Port: 7385, Version: "v-old"})

	res, err := Ensure(w.deps(), EnsureOpts{Root: mkRepoRoot(t), Port: 7385, Version: "v-new", Wait: true})
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if want := []signalCall{{42, syscall.SIGTERM}}; !reflect.DeepEqual(w.signals, want) {
		t.Errorf("signals = %v, want %v", w.signals, want)
	}
	wantSpawn := []spawnCall{{
		exe:     "/usr/local/bin/sdlc",
		args:    []string{"dashboard", "serve", "--port", "7385"},
		logPath: filepath.Join(Dir(), "server.log"),
	}}
	if !reflect.DeepEqual(w.spawns, wantSpawn) {
		t.Errorf("spawns = %v, want %v", w.spawns, wantSpawn)
	}
	want := Result{URL: "http://127.0.0.1:7385", Port: 7385, PID: 99, Started: true, Running: true, Version: "v-new"}
	if res != want {
		t.Errorf("Ensure = %+v, want %+v", res, want)
	}
	if _, err := ReadServerRecord(); !errors.Is(err, ErrNoServer) {
		t.Errorf("old server record not removed after stop: %v", err)
	}
}

func TestEnsurePortDiffersRestarts(t *testing.T) {
	isolateCacheDir(t)
	w := newFakeWorld()
	w.servers[7385] = Health{PID: 42, Version: "v-new"}
	writeRecord(t, ServerRecord{PID: 42, Port: 7385, Version: "v-new"})

	res, err := Ensure(w.deps(), EnsureOpts{Root: mkRepoRoot(t), Port: 7400, Version: "v-new", Wait: true})
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if want := []signalCall{{42, syscall.SIGTERM}}; !reflect.DeepEqual(w.signals, want) {
		t.Errorf("signals = %v, want %v", w.signals, want)
	}
	if len(w.spawns) != 1 || !reflect.DeepEqual(w.spawns[0].args, []string{"dashboard", "serve", "--port", "7400"}) {
		t.Errorf("spawns = %v, want one spawn on port 7400", w.spawns)
	}
	if res.URL != "http://127.0.0.1:7400" || !res.Started || !res.Running {
		t.Errorf("Ensure = %+v, want started and running on 7400", res)
	}
}

func TestEnsureStaleRecordOnForeignPortIgnored(t *testing.T) {
	isolateCacheDir(t)
	w := newFakeWorld()
	w.foreign[7385] = true
	writeRecord(t, ServerRecord{PID: 42, Port: 7385, Version: "v-new"})

	res, err := Ensure(w.deps(), EnsureOpts{Root: mkRepoRoot(t), Port: 7400, Version: "v-new", Wait: true})
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if len(w.signals) != 0 {
		t.Errorf("signals = %v, want none", w.signals)
	}
	if !res.Started || res.Port != 7400 {
		t.Errorf("Ensure = %+v, want a start on 7400", res)
	}
}

func TestEnsureNoWaitDoesNotPoll(t *testing.T) {
	isolateCacheDir(t)
	w := newFakeWorld()

	res, err := Ensure(w.deps(), EnsureOpts{Root: mkRepoRoot(t), Port: 7385, Version: "v-new", Wait: false})
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	want := Result{URL: "http://127.0.0.1:7385", Port: 7385, PID: 99, Started: true, Version: "v-new"}
	if res != want {
		t.Errorf("Ensure = %+v, want %+v", res, want)
	}
	if len(w.healthCalls) != 1 {
		t.Errorf("health calls = %v, want only the pre-start probe", w.healthCalls)
	}
}

// Error table rows (Task 5 Contract): ErrPortForeign, ErrSpawn,
// ErrStartTimeout, ErrUnsupported.
func TestEnsureErrorTable(t *testing.T) {
	tests := []struct {
		name       string
		setup      func(w *fakeWorld)
		wantErr    error
		notErr     error
		suggestion string
		wantSpawns int
	}{
		{
			name:       "port answers but not with sdlc health before start",
			setup:      func(w *fakeWorld) { w.foreign[7385] = true },
			wantErr:    ErrPortForeign,
			suggestion: "Port 7385 is in use by another program. Set port in [dashboard] of ~/.sdlc/local.toml, then run /sdlc:dashboard again.",
			wantSpawns: 0,
		},
		{
			name:       "port answers but not with sdlc health after start",
			setup:      func(w *fakeWorld) { w.spawnForeign = true },
			wantErr:    ErrPortForeign,
			suggestion: "Port 7385 is in use by another program. Set port in [dashboard] of ~/.sdlc/local.toml, then run /sdlc:dashboard again.",
			wantSpawns: 1,
		},
		{
			name:       "start of the process fails",
			setup:      func(w *fakeWorld) { w.spawnErr = errors.New("exec format error") },
			wantErr:    ErrSpawn,
			suggestion: "Read ~/.sdlc-cache/dashboard/server.log, then try again.",
			wantSpawns: 1,
		},
		{
			name:       "executable path unknown",
			setup:      func(w *fakeWorld) { w.exeErr = errors.New("no exe") },
			wantErr:    ErrSpawn,
			suggestion: "Read ~/.sdlc-cache/dashboard/server.log, then try again.",
			wantSpawns: 0,
		},
		{
			name:       "no health answer after 3s",
			setup:      func(w *fakeWorld) { w.spawnAnswers = false },
			wantErr:    ErrStartTimeout,
			suggestion: "Read server.log for the port error, then try again.",
			wantSpawns: 1,
		},
		{
			name:       "OS is not Unix",
			setup:      func(w *fakeWorld) { w.spawnErr = ErrUnsupported },
			wantErr:    ErrUnsupported,
			notErr:     ErrSpawn,
			suggestion: "The dashboard runs only on macOS and Linux.",
			wantSpawns: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isolateCacheDir(t)
			w := newFakeWorld()
			tt.setup(w)

			_, err := Ensure(w.deps(), EnsureOpts{Root: mkRepoRoot(t), Port: 7385, Version: "v-new", Wait: true})
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Ensure error = %v, want %v", err, tt.wantErr)
			}
			if tt.notErr != nil && errors.Is(err, tt.notErr) {
				t.Errorf("Ensure error = %v, must not match %v", err, tt.notErr)
			}
			if got := suggestionOf(t, err); got != tt.suggestion {
				t.Errorf("Suggestion = %q, want %q", got, tt.suggestion)
			}
			if len(w.spawns) != tt.wantSpawns {
				t.Errorf("spawns = %d, want %d", len(w.spawns), tt.wantSpawns)
			}
		})
	}
}

func TestEnsureStartTimeoutPollsEvery100msFor3s(t *testing.T) {
	isolateCacheDir(t)
	w := newFakeWorld()
	w.spawnAnswers = false
	var slept []time.Duration
	d := w.deps()
	d.Sleep = func(dur time.Duration) { slept = append(slept, dur) }

	_, err := Ensure(d, EnsureOpts{Root: mkRepoRoot(t), Port: 7385, Version: "v-new", Wait: true})
	if !errors.Is(err, ErrStartTimeout) {
		t.Fatalf("Ensure error = %v, want ErrStartTimeout", err)
	}
	if len(slept) != 30 {
		t.Errorf("sleeps = %d, want 30 (3s / 100ms)", len(slept))
	}
	for _, s := range slept {
		if s != 100*time.Millisecond {
			t.Fatalf("sleep = %v, want 100ms", s)
		}
	}
	// One pre-start probe plus 30 polls.
	if len(w.healthCalls) != 31 {
		t.Errorf("health calls = %d, want 31", len(w.healthCalls))
	}
}

func TestStopSignalsOnlyHealthPID(t *testing.T) {
	isolateCacheDir(t)
	w := newFakeWorld()
	// The record names pid 11, but the server on the port answers as pid 42.
	writeRecord(t, ServerRecord{PID: 11, Port: 7385, Version: "v1"})
	w.servers[7385] = Health{PID: 42, Version: "v1"}

	res, err := Stop(w.deps())
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if want := []signalCall{{42, syscall.SIGTERM}}; !reflect.DeepEqual(w.signals, want) {
		t.Errorf("signals = %v, want %v", w.signals, want)
	}
	want := Result{URL: "http://127.0.0.1:7385", Port: 7385, PID: 42, Version: "v1"}
	if res != want {
		t.Errorf("Stop = %+v, want %+v", res, want)
	}
}

func TestStopNoHealthAnswerSendsNoSignal(t *testing.T) {
	isolateCacheDir(t)
	w := newFakeWorld()
	writeRecord(t, ServerRecord{PID: 11, Port: 7385, Version: "v1"})

	res, err := Stop(w.deps())
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if len(w.signals) != 0 {
		t.Errorf("signals = %v, want none", w.signals)
	}
	if res.Running {
		t.Errorf("Stop = %+v, want Running:false", res)
	}
	if _, err := ReadServerRecord(); !errors.Is(err, ErrNoServer) {
		t.Errorf("stale record not removed: %v", err)
	}
}

func TestStopServerStillAnswersReportsRunning(t *testing.T) {
	isolateCacheDir(t)
	w := newFakeWorld()
	w.ignoreSignal = true
	writeRecord(t, ServerRecord{PID: 42, Port: 7385, Version: "v1"})
	w.servers[7385] = Health{PID: 42, Version: "v1"}

	res, err := Stop(w.deps())
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if !res.Running {
		t.Errorf("Stop = %+v, want Running:true when the server still answers", res)
	}
	if w.sleeps != 20 {
		t.Errorf("sleeps = %d, want 20 (2s / 100ms)", w.sleeps)
	}
}

func TestStopNoRecord(t *testing.T) {
	isolateCacheDir(t)
	w := newFakeWorld()

	res, err := Stop(w.deps())
	if err != nil || res != (Result{}) {
		t.Errorf("Stop = %+v, %v; want zero Result, nil", res, err)
	}
	if len(w.healthCalls) != 0 || len(w.signals) != 0 {
		t.Errorf("health=%v signals=%v, want none", w.healthCalls, w.signals)
	}
}

func TestStatus(t *testing.T) {
	t.Run("no record", func(t *testing.T) {
		isolateCacheDir(t)
		res, err := Status(newFakeWorld().deps())
		if err != nil || res.Running {
			t.Errorf("Status = %+v, %v; want Running:false, nil", res, err)
		}
	})
	t.Run("record and health answer", func(t *testing.T) {
		isolateCacheDir(t)
		w := newFakeWorld()
		writeRecord(t, ServerRecord{PID: 42, Port: 7385, Version: "v1"})
		w.servers[7385] = Health{PID: 42, Version: "v1"}
		res, err := Status(w.deps())
		want := Result{URL: "http://127.0.0.1:7385", Port: 7385, PID: 42, Running: true, Version: "v1"}
		if err != nil || res != want {
			t.Errorf("Status = %+v, %v; want %+v", res, err, want)
		}
	})
	t.Run("record but no health answer", func(t *testing.T) {
		isolateCacheDir(t)
		w := newFakeWorld()
		writeRecord(t, ServerRecord{PID: 42, Port: 7385, Version: "v1"})
		res, err := Status(w.deps())
		if err != nil || res.Running {
			t.Errorf("Status = %+v, %v; want Running:false, nil", res, err)
		}
	})
}

// TestHTTPHealth exercises the production Deps.Health over real HTTP on the
// loopback address.
func TestHTTPHealth(t *testing.T) {
	serve := func(t *testing.T, status int, body string) int {
		t.Helper()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/health" {
				http.NotFound(w, r)
				return
			}
			w.WriteHeader(status)
			_, _ = w.Write([]byte(body))
		}))
		t.Cleanup(srv.Close)
		return srv.Listener.Addr().(*net.TCPAddr).Port
	}

	t.Run("sdlc health body", func(t *testing.T) {
		port := serve(t, 200, `{"pid":42,"version":"v1","startedAt":"2026-10-07T12:00:00Z"}`)
		h, err := DefaultDeps().Health(port, time.Second)
		if err != nil {
			t.Fatalf("Health: %v", err)
		}
		if h.PID != 42 || h.Version != "v1" || !h.StartedAt.Equal(time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)) {
			t.Errorf("Health = %+v", h)
		}
	})
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"not found", 404, "nope"},
		{"html page", 200, "<html>hello</html>"},
		{"json without pid", 200, `{"version":"v1"}`},
		{"json without version", 200, `{"pid":42}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			port := serve(t, tc.status, tc.body)
			if _, err := DefaultDeps().Health(port, time.Second); !errors.Is(err, ErrPortForeign) {
				t.Errorf("Health error = %v, want ErrPortForeign", err)
			}
		})
	}
	t.Run("no answer", func(t *testing.T) {
		srv := httptest.NewServer(http.NotFoundHandler())
		port := srv.Listener.Addr().(*net.TCPAddr).Port
		srv.Close()
		_, err := DefaultDeps().Health(port, time.Second)
		if err == nil || errors.Is(err, ErrPortForeign) {
			t.Errorf("Health error = %v, want a non-foreign error", err)
		}
	})
}

// TestSpawnDetachedWritesLog starts a short real process through the
// production spawn to prove the log wiring and the detached start.
func TestSpawnDetachedWritesLog(t *testing.T) {
	if runtime.GOOS == "windows" {
		if _, err := spawnDetached("x", nil, filepath.Join(t.TempDir(), "x.log")); !errors.Is(err, ErrUnsupported) {
			t.Fatalf("spawnDetached error = %v, want ErrUnsupported", err)
		}
		return
	}
	logPath := filepath.Join(t.TempDir(), "sub", "server.log")
	pid, err := spawnDetached("/bin/sh", []string{"-c", "echo dashboard-spawn-ok"}, logPath)
	if err != nil {
		t.Fatalf("spawnDetached: %v", err)
	}
	if pid <= 0 {
		t.Fatalf("pid = %d, want > 0", pid)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		data, _ := os.ReadFile(logPath)
		if strings.Contains(string(data), "dashboard-spawn-ok") {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("log %s never got the process output", logPath)
}
