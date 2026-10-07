package dashboard

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
)

// Sentinel errors for the dashboard control flow. Ensure returns them wrapped
// in a *mcpserver.DomainError whose Suggestion tells the user what to do next;
// errors.Is matches the sentinel through DomainError.Unwrap.
var (
	// ErrPortForeign: the port answers, but not with an sdlc health body.
	ErrPortForeign = errors.New("dashboard: port is in use by another program")
	// ErrSpawn: the start of the server process failed.
	ErrSpawn = errors.New("dashboard: server process did not start")
	// ErrStartTimeout: the started server gave no health answer in time.
	ErrStartTimeout = errors.New("dashboard: server gave no health answer")
	// ErrUnsupported: the OS is not Unix, so no detached server can start.
	ErrUnsupported = errors.New("dashboard: unsupported operating system")
)

const (
	// probeTimeout is the per-call timeout for a health probe.
	probeTimeout = 500 * time.Millisecond
	// pollInterval is the wait between two health calls in a poll loop.
	pollInterval = 100 * time.Millisecond
	// stopPolls is how many pollInterval waits Stop allows (2 s) for the old
	// server to stop answering.
	stopPolls = 20
	// startPolls is how many pollInterval waits Ensure allows (3 s) for a new
	// server to answer.
	startPolls = 30
	// healthBodyLimit caps how much of a health answer is read.
	healthBodyLimit = 64 << 10
)

// Deps holds every external dependency of Ensure, Status, and Stop as a
// function field, so tests inject fakes and start no real process.
// Production code uses DefaultDeps.
type Deps struct {
	// Spawn starts exe with args as a detached process (new session), with
	// stdout and stderr appended to logPath, and returns its pid. On a non-Unix
	// OS it returns ErrUnsupported.
	Spawn func(exe string, args []string, logPath string) (pid int, err error)
	// Health calls GET /api/health on 127.0.0.1:port. It returns an error
	// wrapping ErrPortForeign when the port answers but the answer is not an
	// sdlc health body (status other than 200, not JSON, pid <= 0, or empty
	// version). Any other error means the port gave no answer.
	Health func(port int, timeout time.Duration) (Health, error)
	// Signal sends sig to the process pid.
	Signal func(pid int, sig os.Signal) error
	// Exe returns the path of the running binary (os.Executable).
	Exe func() (string, error)
	// Sleep waits for d (time.Sleep).
	Sleep func(time.Duration)
}

// Health is the body of the dashboard server's GET /api/health answer.
type Health struct {
	PID       int       `json:"pid"`
	Version   string    `json:"version"`
	StartedAt time.Time `json:"startedAt"`
}

// EnsureOpts configures Ensure.
type EnsureOpts struct {
	// Root is the repo root to register. Callers pass worktree.MainRoot(),
	// never worktree.ActiveRoot().
	Root string
	// Port is the configured dashboard port.
	Port int
	// Version is the version of the running binary.
	Version string
	// Wait makes Ensure poll health until the new server answers.
	Wait bool
}

// Result describes the dashboard server after Ensure, Status, or Stop.
// Running is true only when a health call confirmed the server. Started is
// true when this call spawned a new server process; with Wait:false a fresh
// start reports Started:true, Running:false.
type Result struct {
	URL     string
	Port    int
	PID     int
	Started bool
	Running bool
	Version string
}

// serverURL returns the loopback address of a dashboard server on port.
func serverURL(port int) string {
	return fmt.Sprintf("http://127.0.0.1:%d", port)
}

// Ensure makes sure a dashboard server of o.Version listens on o.Port. It
// registers o.Root, probes the recorded server and the configured port,
// stops an sdlc server of another version or port, and spawns
// "<exe> dashboard serve --port <P>" when no matching server answers.
func Ensure(d Deps, o EnsureOpts) (Result, error) {
	if err := RegisterRoot(o.Root, time.Now()); err != nil {
		return Result{}, err
	}

	// Probe the recorded server's port first (when it differs), then the
	// configured port. A stale record whose port another program now holds
	// is ignored; the configured port must be free or ours.
	ports := []int{}
	if rec, err := ReadServerRecord(); err == nil && rec.Port != 0 && rec.Port != o.Port {
		ports = append(ports, rec.Port)
	}
	ports = append(ports, o.Port)

	for _, port := range ports {
		h, err := d.Health(port, probeTimeout)
		switch {
		case err == nil:
			if port == o.Port && h.Version == o.Version {
				return Result{
					URL:     serverURL(port),
					Port:    port,
					PID:     h.PID,
					Running: true,
					Version: h.Version,
				}, nil
			}
			stopServer(d, port, h.PID)
		case errors.Is(err, ErrPortForeign):
			if port == o.Port {
				return Result{}, portForeignError(o.Port)
			}
		}
	}

	exe, err := d.Exe()
	if err != nil {
		return Result{}, spawnError(err)
	}
	args := []string{"dashboard", "serve", "--port", fmt.Sprint(o.Port)}
	pid, err := d.Spawn(exe, args, filepath.Join(Dir(), "server.log"))
	if err != nil {
		if errors.Is(err, ErrUnsupported) {
			return Result{}, unsupportedError()
		}
		return Result{}, spawnError(err)
	}

	res := Result{URL: serverURL(o.Port), Port: o.Port, PID: pid, Started: true, Version: o.Version}
	if !o.Wait {
		return res, nil
	}

	for i := 0; i < startPolls; i++ {
		h, err := d.Health(o.Port, probeTimeout)
		if err == nil {
			// A concurrent start from another session can win the port; the
			// answering server is the one that counts.
			res.PID = h.PID
			res.Version = h.Version
			res.Running = true
			return res, nil
		}
		if errors.Is(err, ErrPortForeign) {
			return Result{}, portForeignError(o.Port)
		}
		d.Sleep(pollInterval)
	}
	return Result{}, startTimeoutError(o.Port)
}

// Status reports the recorded dashboard server. Running is false when no
// server record exists or the recorded port gives no sdlc health answer.
func Status(d Deps) (Result, error) {
	rec, err := ReadServerRecord()
	if err != nil {
		if errors.Is(err, ErrNoServer) {
			return Result{}, nil
		}
		return Result{}, err
	}
	h, err := d.Health(rec.Port, probeTimeout)
	if err != nil {
		return Result{URL: serverURL(rec.Port), Port: rec.Port}, nil
	}
	return Result{
		URL:     serverURL(rec.Port),
		Port:    rec.Port,
		PID:     h.PID,
		Running: true,
		Version: h.Version,
	}, nil
}

// Stop ends the recorded dashboard server. It sends SIGTERM only to the pid
// from the /api/health answer, never to the pid in the record: a stale record
// can name a pid the OS has since given to another process. Running is true
// in the result only when the server still answered after the 2 s wait.
func Stop(d Deps) (Result, error) {
	rec, err := ReadServerRecord()
	if err != nil {
		if errors.Is(err, ErrNoServer) {
			return Result{}, nil
		}
		return Result{}, err
	}
	res := Result{URL: serverURL(rec.Port), Port: rec.Port}
	h, err := d.Health(rec.Port, probeTimeout)
	if err != nil {
		// No sdlc server answers: the record is stale. RemoveServerRecord is
		// pid-guarded, so it cannot delete a newer server's record.
		_ = RemoveServerRecord(rec.PID)
		return res, nil
	}
	res.PID = h.PID
	res.Version = h.Version
	res.Running = !stopServer(d, rec.Port, h.PID)
	return res, nil
}

// stopServer sends SIGTERM to pid (the pid from a health answer on port) and
// waits up to 2 s for port to stop giving an sdlc health answer. It reports
// whether the server stopped. On success it removes the server record when
// the record still names pid.
func stopServer(d Deps, port, pid int) bool {
	if err := d.Signal(pid, syscall.SIGTERM); err != nil {
		return false
	}
	for i := 0; i < stopPolls; i++ {
		if _, err := d.Health(port, probeTimeout); err != nil {
			_ = RemoveServerRecord(pid)
			return true
		}
		d.Sleep(pollInterval)
	}
	return false
}

func portForeignError(port int) error {
	return &mcpserver.DomainError{
		Msg: fmt.Sprintf("dashboard: port %d answers, but not with sdlc health", port),
		Suggestion: fmt.Sprintf(
			"Port %d is in use by another program. Set port in [dashboard] of ~/.sdlc/local.toml, then run /sdlc:dashboard again.",
			port),
		Cause: ErrPortForeign,
	}
}

func spawnError(cause error) error {
	return &mcpserver.DomainError{
		Msg:        fmt.Sprintf("dashboard: start of the server process failed: %v", cause),
		Suggestion: "Read ~/.sdlc-cache/dashboard/server.log, then try again.",
		Cause:      fmt.Errorf("%w: %w", ErrSpawn, cause),
	}
}

func startTimeoutError(port int) error {
	return &mcpserver.DomainError{
		Msg:        fmt.Sprintf("dashboard: no health answer on port %d after 3s", port),
		Suggestion: "Read server.log for the port error, then try again.",
		Cause:      ErrStartTimeout,
	}
}

func unsupportedError() error {
	return &mcpserver.DomainError{
		Msg:        fmt.Sprintf("dashboard: %s is not supported", runtime.GOOS),
		Suggestion: "The dashboard runs only on macOS and Linux.",
		Cause:      ErrUnsupported,
	}
}

// DefaultDeps wires Deps to the real process, HTTP, and clock functions.
func DefaultDeps() Deps {
	return Deps{
		Spawn:  spawnDetached,
		Health: httpHealth,
		Signal: func(pid int, sig os.Signal) error {
			p, err := os.FindProcess(pid)
			if err != nil {
				return err
			}
			return p.Signal(sig)
		},
		Exe:   os.Executable,
		Sleep: time.Sleep,
	}
}

// httpHealth is the production Deps.Health: GET http://127.0.0.1:port/api/health.
func httpHealth(port int, timeout time.Duration) (Health, error) {
	client := &http.Client{Timeout: timeout}
	resp, err := client.Get(serverURL(port) + "/api/health")
	if err != nil {
		return Health{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Health{}, fmt.Errorf("%w: status %d", ErrPortForeign, resp.StatusCode)
	}
	var h Health
	if err := json.NewDecoder(io.LimitReader(resp.Body, healthBodyLimit)).Decode(&h); err != nil {
		return Health{}, fmt.Errorf("%w: %v", ErrPortForeign, err)
	}
	if h.PID <= 0 || h.Version == "" {
		return Health{}, fmt.Errorf("%w: body is not an sdlc health answer", ErrPortForeign)
	}
	return h, nil
}

// OpenBrowser opens url in the default browser: open on macOS, xdg-open on
// Linux. Other systems return ErrUnsupported.
func OpenBrowser(url string) error {
	var name string
	switch runtime.GOOS {
	case "darwin":
		name = "open"
	case "linux":
		name = "xdg-open"
	default:
		return ErrUnsupported
	}
	cmd := exec.Command(name, url)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("dashboard: %s %s: %w", name, url, err)
	}
	// Reap the launcher so it does not stay as a zombie in a long-lived
	// MCP process.
	go func() { _ = cmd.Wait() }()
	return nil
}
