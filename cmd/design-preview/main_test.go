package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/dashboard"
)

// runServingPattern matches the serving line and captures the port and the pid.
var runServingPattern = regexp.MustCompile(`^design preview: serving http://127\.0\.0\.1:(\d+)/ \(pid (\d+)\)$`)

// runClient is the client of the tests. Its time limit ends a test that hangs.
var runClient = &http.Client{Timeout: 5 * time.Second}

// runServing is a run() in a goroutine that has printed its serving line.
type runServing struct {
	base   string        // http://127.0.0.1:<port>
	port   int           // the port that run chose
	lines  []string      // every stdout line up to and including the serving line
	done   chan int      // receives the exit code of run
	stderr *bytes.Buffer // what run wrote to stderr; read it only after done
}

// runStart runs run() on "127.0.0.1:0" in a goroutine and reads its stdout
// through an io.Pipe until the serving line. It fails the test when run ends
// first or prints no serving line within 10 s. At the end of the test it posts
// to the stop route, so a failed test does not leave a server behind.
func runStart(t *testing.T, repo string, args []string, read func() (dashboard.ServerRecord, error)) *runServing {
	t.Helper()
	pr, pw := io.Pipe()
	s := &runServing{done: make(chan int, 1), stderr: &bytes.Buffer{}}
	go func() {
		s.done <- run(args, runConfig{RepoRoot: repo, Addr: "127.0.0.1:0", ReadRecord: read}, pw, s.stderr)
		_ = pw.Close()
	}()

	lines := make(chan string, 16)
	go func() {
		scanner := bufio.NewScanner(pr)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
		close(lines)
	}()

	timeout := time.After(10 * time.Second)
	for {
		select {
		case line, ok := <-lines:
			if !ok {
				t.Fatalf("run ended without a serving line\nstdout: %q\nstderr: %q", s.lines, s.stderr)
			}
			s.lines = append(s.lines, line)
			m := runServingPattern.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			s.port, _ = strconv.Atoi(m[1])
			s.base = "http://127.0.0.1:" + m[1]
			if pid, _ := strconv.Atoi(m[2]); pid != os.Getpid() {
				t.Errorf("serving line pid = %d, want %d", pid, os.Getpid())
			}
			t.Cleanup(func() {
				// Best effort: the test may have stopped the server already.
				if resp, err := runClient.Post(s.base+"/__design/stop", "", nil); err == nil {
					_ = resp.Body.Close()
				}
			})

			return s
		case <-timeout:
			t.Fatalf("no serving line within 10 s\nstdout: %q", s.lines)
		}
	}
}

// stop posts to the stop route and returns the exit code of run. It fails the
// test when the route does not answer 202 or when run does not return within 3 s.
func (s *runServing) stop(t *testing.T) int {
	t.Helper()
	resp, err := runClient.Post(s.base+"/__design/stop", "", nil)
	if err != nil {
		t.Fatalf("POST /__design/stop: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Errorf("POST /__design/stop status = %d, want 202", resp.StatusCode)
	}

	select {
	case code := <-s.done:
		return code
	case <-time.After(3 * time.Second):
		t.Fatal("run did not return within 3 s after the stop route")
		return -1
	}
}

// runGet fetches url and returns the status and the body.
func runGet(t *testing.T, url string) (int, string) {
	t.Helper()
	resp, err := runClient.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read GET %s: %v", url, err)
	}

	return resp.StatusCode, string(body)
}

// runFreeAddr returns a 127.0.0.1 address that is free now. A test uses it to
// check that run closed its listener, because the address is known before run.
func runFreeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	return addr
}

// runAssertFree fails the test when addr cannot be bound, which means a
// listener on it is still open.
func runAssertFree(t *testing.T, addr string) {
	t.Helper()
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Errorf("listener on %s is still open: %v", addr, err)
		return
	}
	_ = ln.Close()
}

// runOnce calls run() with args on addr and returns the exit code, stdout and stderr.
func runOnce(repo, addr string, args ...string) (code int, stdout, stderr string) {
	var out, errOut bytes.Buffer
	code = run(args, runConfig{RepoRoot: repo, Addr: addr}, &out, &errOut)

	return code, out.String(), errOut.String()
}

// runErrorLine gives the stderr text of one error line.
func runErrorLine(text string) string {
	return "design preview: error — " + text + "\n"
}

// TestRun covers each exit code and each printed line of run() on real
// listeners at 127.0.0.1 and in real git repos.
func TestRun(t *testing.T) {
	t.Run("prints the start-rule line, then the serving line, and serves until the stop route", func(t *testing.T) {
		repo := startRepo(t)
		d := proxyFakeDashboard(t, 0, 0)
		s := runStart(t, repo, nil, proxyRecord(d.srv.URL))

		wantLines := []string{
			"design preview: case a — no draft. Copied the shipped page. Base commit " + startHead(t, repo) + ".",
			fmt.Sprintf("design preview: serving http://127.0.0.1:%d/ (pid %d)", s.port, os.Getpid()),
		}
		if !reflect.DeepEqual(s.lines, wantLines) {
			t.Errorf("stdout lines\ngot:  %q\nwant: %q", s.lines, wantLines)
		}

		// The page routes read the draft folder of the repo, not the shipped page.
		startWrite(t, repo, draftDir+"/static/app.css", "body { color: blue; }\n")
		if status, body := runGet(t, s.base+"/static/app.css"); status != http.StatusOK || body != "body { color: blue; }\n" {
			t.Errorf("GET /static/app.css = %d %q, want 200 of the draft file", status, body)
		}
		// The API routes reach the dashboard that ReadRecord names.
		if status, body := runGet(t, s.base+"/api/snapshot"); status != http.StatusOK || body != proxySnapshotBody {
			t.Errorf("GET /api/snapshot = %d %q, want 200 %q", status, body, proxySnapshotBody)
		}
		wantStatus := `{"dashboard":"running","url":"` + d.srv.URL + `"}`
		if status, body := runGet(t, s.base+"/__design/status"); status != http.StatusOK || body != wantStatus {
			t.Errorf("GET /__design/status = %d %q, want 200 %q", status, body, wantStatus)
		}

		if code := s.stop(t); code != 0 {
			t.Errorf("exit code = %d, want 0", code)
		}
		if got := s.stderr.String(); got != "" {
			t.Errorf("stderr = %q, want empty", got)
		}
		if resp, err := runClient.Get(s.base + "/"); err == nil {
			_ = resp.Body.Close()
			t.Error("the preview still answers after the stop route")
		}
	})

	t.Run("the stop route ends run with exit code 0 within 3 s while an event stream is open", func(t *testing.T) {
		repo := startRepo(t)
		d := proxyFakeDashboard(t, 0, 0)
		s := runStart(t, repo, nil, proxyRecord(d.srv.URL))

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.base+"/api/events", nil)
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("GET /api/events: %v", err)
		}
		defer resp.Body.Close()
		if first, err := bufio.NewReader(resp.Body).ReadString('\n'); err != nil || first != "data: one\n" {
			t.Fatalf("first stream line = %q, %v, want %q", first, err, "data: one\n")
		}

		// The stream stays open: the fake dashboard sends its next event only after release.
		if code := s.stop(t); code != 0 {
			t.Errorf("exit code = %d, want 0", code)
		}
		// Close ended the connection, so the body ends before the 10 s limit.
		_, _ = io.ReadAll(resp.Body)
		if ctx.Err() != nil {
			t.Error("the event stream was still open after run returned")
		}
	})

	t.Run("the mode flag reaches the start rule", func(t *testing.T) {
		tests := []struct {
			name  string
			args  []string
			setup func(t *testing.T, repo string)
			want  func(t *testing.T, repo string) string
		}{
			{
				name: "fresh",
				args: []string{"-mode", "fresh"},
				want: func(t *testing.T, repo string) string {
					return "design preview: fresh — copied the shipped page. Base commit " + startHead(t, repo) + "."
				},
			},
			{
				name:  "continue",
				args:  []string{"-mode", "continue"},
				setup: func(t *testing.T, repo string) { startRun(t, repo, ModeAuto) },
				want: func(t *testing.T, repo string) string {
					return "design preview: continue — kept the draft. Base commit " + startHead(t, repo) + "."
				},
			},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				repo := startRepo(t)
				if tc.setup != nil {
					tc.setup(t, repo)
				}
				d := proxyFakeDashboard(t, 0, 0)
				s := runStart(t, repo, tc.args, proxyRecord(d.srv.URL))

				if len(s.lines) != 2 || s.lines[0] != tc.want(t, repo) {
					t.Errorf("stdout lines = %q, want the %s line, then the serving line", s.lines, tc.name)
				}
				if code := s.stop(t); code != 0 {
					t.Errorf("exit code = %d, want 0", code)
				}
			})
		}
	})

	t.Run("case d prints its lines, exits 3 without a serving line and closes the listener", func(t *testing.T) {
		repo := startRepo(t)
		base := startDraftWithChange(t, repo)
		startWrite(t, repo, draftDir+"/static/app.css", "body { color: blue; }\n")
		before := startSnapshot(t, repo)
		addr := runFreeAddr(t)

		code, stdout, stderr := runOnce(repo, addr)

		if code != 3 {
			t.Errorf("exit code = %d, want 3", code)
		}
		if want := strings.Join(startCaseDLines(t, repo, base), "\n") + "\n"; stdout != want {
			t.Errorf("stdout\ngot:  %q\nwant: %q", stdout, want)
		}
		if strings.Contains(stdout, "serving") {
			t.Errorf("stdout has a serving line: %q", stdout)
		}
		if stderr != "" {
			t.Errorf("stderr = %q, want empty", stderr)
		}
		startAssertUnchanged(t, repo, before)
		runAssertFree(t, addr)
	})

	t.Run("a start-rule error prints its error line, exits 2 and closes the listener", func(t *testing.T) {
		tests := []struct {
			name    string
			args    []string
			setup   func(t *testing.T, repo string)
			wantErr error
		}{
			{
				name: "uncommitted change in the shipped page",
				setup: func(t *testing.T, repo string) {
					startWrite(t, repo, shippedPath+"/app.css", "body { color: red; }\n")
				},
				wantErr: errDirty,
			},
			{
				name:    "continue without a draft",
				args:    []string{"-mode", "continue"},
				wantErr: errNoDraft,
			},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				repo := startRepo(t)
				if tc.setup != nil {
					tc.setup(t, repo)
				}
				addr := runFreeAddr(t)

				code, stdout, stderr := runOnce(repo, addr, tc.args...)

				if code != 2 {
					t.Errorf("exit code = %d, want 2", code)
				}
				if want := runErrorLine(tc.wantErr.Error()); stderr != want {
					t.Errorf("stderr\ngot:  %q\nwant: %q", stderr, want)
				}
				if stdout != "" {
					t.Errorf("stdout = %q, want empty", stdout)
				}
				runAssertFree(t, addr)
			})
		}
	})

	t.Run("a bound address prints the in-use line, exits 2 and leaves the draft alone", func(t *testing.T) {
		repo := startRepo(t)
		held, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("listen: %v", err)
		}
		defer held.Close()
		addr := held.Addr().String()

		code, stdout, stderr := runOnce(repo, addr)

		if code != 2 {
			t.Errorf("exit code = %d, want 2", code)
		}
		want := runErrorLine(fmt.Sprintf("%s is in use. A preview may still run: http://%s/", addr, addr))
		if stderr != want {
			t.Errorf("stderr\ngot:  %q\nwant: %q", stderr, want)
		}
		if stdout != "" {
			t.Errorf("stdout = %q, want empty", stdout)
		}
		// The listen comes before the start rule, so a running preview keeps its draft.
		if startExists(repo, "design") {
			t.Error("the design folder exists: the start rule ran before the listen")
		}
	})

	t.Run("another listen error names the address and the cause", func(t *testing.T) {
		repo := startRepo(t)

		code, stdout, stderr := runOnce(repo, "127.0.0.1:99999")

		if code != 2 {
			t.Errorf("exit code = %d, want 2", code)
		}
		if want := runErrorLine("127.0.0.1:99999: address 99999: invalid port"); stderr != want {
			t.Errorf("stderr\ngot:  %q\nwant: %q", stderr, want)
		}
		if stdout != "" {
			t.Errorf("stdout = %q, want empty", stdout)
		}
	})

	t.Run("a bad flag prints the allowed values, exits 2 and changes nothing", func(t *testing.T) {
		tests := []struct {
			name string
			args []string
			want string
		}{
			{"unknown mode", []string{"-mode", "bogus"}, `unknown mode "bogus". Allowed: auto, fresh, continue.`},
			{"empty mode", []string{"-mode", ""}, `unknown mode "". Allowed: auto, fresh, continue.`},
			{"mode without a value", []string{"-mode"}, "flag needs an argument: -mode. Usage: -mode auto|fresh|continue."},
			{"unknown flag", []string{"-port", "1"}, "flag provided but not defined: -port. Usage: -mode auto|fresh|continue."},
			{"extra argument", []string{"extra"}, `unexpected argument "extra". Usage: -mode auto|fresh|continue.`},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				repo := startRepo(t)
				before := startSnapshot(t, repo)

				code, stdout, stderr := runOnce(repo, "127.0.0.1:0", tc.args...)

				if code != 2 {
					t.Errorf("exit code = %d, want 2", code)
				}
				if want := runErrorLine(tc.want); stderr != want {
					t.Errorf("stderr\ngot:  %q\nwant: %q", stderr, want)
				}
				if stdout != "" {
					t.Errorf("stdout = %q, want empty", stdout)
				}
				startAssertUnchanged(t, repo, before)
			})
		}
	})
}

// TestServeFailure covers the Serve error branch: a listener that is already
// closed makes Serve fail at once with an error other than ErrServerClosed.
func TestServeFailure(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	var stdout, stderr bytes.Buffer
	code := serve(ln, runConfig{RepoRoot: t.TempDir(), Addr: addr, ReadRecord: proxyRecordErr(dashboard.ErrNoServer)}, &stdout, &stderr)

	if code != exitError {
		t.Errorf("exit code = %d, want %d", code, exitError)
	}
	if want := "design preview: error — " + addr + ": "; !strings.HasPrefix(stderr.String(), want) {
		t.Errorf("stderr = %q, want prefix %q", stderr.String(), want)
	}
}

// TestMainBinary builds the program and runs it, so main() runs with the real
// repo lookup, listenAddr and dashboard.ReadServerRecord. It covers the two
// exits that need no free fixed port: outside a git repo, and with listenAddr
// held. Exit codes are what the design tasks of the Taskfile read.
func TestMainBinary(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the program")
	}
	startClearGitEnv(t)
	bin := filepath.Join(t.TempDir(), "design-preview")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}

	runBin := func(t *testing.T, dir string) (int, string, string) {
		t.Helper()
		var stdout, stderr bytes.Buffer
		cmd := exec.Command(bin)
		cmd.Dir = dir
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err := cmd.Run()
		var exitErr *exec.ExitError
		switch {
		case err == nil:
			return 0, stdout.String(), stderr.String()
		case errors.As(err, &exitErr):
			return exitErr.ExitCode(), stdout.String(), stderr.String()
		}
		t.Fatalf("run %s: %v", bin, err)
		return 0, "", ""
	}

	t.Run("outside a git repo it prints the git error and exits 2", func(t *testing.T) {
		code, stdout, stderr := runBin(t, t.TempDir())
		if code != exitError {
			t.Errorf("exit code = %d, want %d", code, exitError)
		}
		if !strings.HasPrefix(stderr, "design preview: error — ") || !strings.Contains(stderr, "not a git repository") {
			t.Errorf("stderr = %q, want the git error line", stderr)
		}
		if stdout != "" {
			t.Errorf("stdout = %q, want empty", stdout)
		}
	})

	t.Run("with listenAddr held it prints the in-use line, exits 2 and changes nothing", func(t *testing.T) {
		repo := startRepo(t)
		// A listen that fails means another program holds the address, which
		// gives the same in-use line.
		if held, err := net.Listen("tcp", listenAddr); err == nil {
			t.Cleanup(func() { _ = held.Close() })
		}
		before := startSnapshot(t, repo)

		code, stdout, stderr := runBin(t, repo)

		if code != exitError {
			t.Errorf("exit code = %d, want %d", code, exitError)
		}
		if want := runErrorLine(fmt.Sprintf("%s is in use. A preview may still run: http://%s/", listenAddr, listenAddr)); stderr != want {
			t.Errorf("stderr\ngot:  %q\nwant: %q", stderr, want)
		}
		if stdout != "" {
			t.Errorf("stdout = %q, want empty", stdout)
		}
		startAssertUnchanged(t, repo, before)
	})
}
