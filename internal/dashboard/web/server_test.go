package web

import (
	"bufio"
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/dashboard"
	"github.com/rnagrodzki/sdlc-plugin/internal/tools"
)

const (
	testPort    = 7385
	testToken   = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	testHost    = "127.0.0.1:7385"
	testOrigin  = "http://127.0.0.1:7385"
	testVersion = "v-test"
)

// isolateCacheDir points dashboard.Dir() at a temp directory, so no test
// touches the developer's real ~/.sdlc-cache/dashboard/server.json.
func isolateCacheDir(t *testing.T) {
	t.Helper()
	t.Setenv("SDLC_CACHE_DIR", t.TempDir())
}

// shortTimings shortens the poll and ping intervals for the test and puts
// the old values back after it.
func shortTimings(t *testing.T, poll, ping time.Duration) {
	t.Helper()
	oldPoll, oldPing := snapshotPollInterval, pingInterval
	snapshotPollInterval, pingInterval = poll, ping
	t.Cleanup(func() { snapshotPollInterval, pingInterval = oldPoll, oldPing })
}

// captureStderr sends the package's stderr lines to a buffer for the test.
func captureStderr(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	old := stderr
	stderr = &buf
	t.Cleanup(func() { stderr = old })
	return &buf
}

// fakeCollector returns a snapshot of one repo whose name the test can
// change. GeneratedAt changes at every call, like the real collector.
type fakeCollector struct {
	mu    sync.Mutex
	repo  string
	calls int
}

func (f *fakeCollector) collect(roots []string, at time.Time) tools.DashboardSnapshot {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return tools.DashboardSnapshot{
		GeneratedAt: time.Now().Format(time.RFC3339Nano),
		Version:     testVersion,
		Repos:       []tools.DashboardRepo{{Root: strings.Join(roots, ","), Name: f.repo}},
	}
}

func (f *fakeCollector) setRepo(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.repo = name
}

func (f *fakeCollector) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// testOptions returns Options with fakes for every dependency. Listen and
// Health fail the test unless the test replaces them.
func testOptions(t *testing.T, c *fakeCollector) Options {
	t.Helper()
	return Options{
		Port:    testPort,
		Version: testVersion,
		Token:   testToken,
		Roots: func(time.Time) ([]dashboard.Root, error) {
			return []dashboard.Root{{Root: "/repo/a"}}, nil
		},
		Collect: c.collect,
		Listen: func(string, string) (net.Listener, error) {
			t.Error("Listen called")
			return nil, errors.New("unexpected Listen")
		},
		Health: func(int, time.Duration) (dashboard.Health, error) {
			t.Error("Health called")
			return dashboard.Health{}, errors.New("unexpected Health")
		},
	}
}

// newTestHandler builds the request handler without a listener.
func newTestHandler(t *testing.T, stop func()) (*handler, *fakeCollector) {
	t.Helper()
	c := &fakeCollector{repo: "a"}
	if stop == nil {
		stop = func() {}
	}
	return newHandler(context.Background(), testOptions(t, c), testToken, 4242, time.Unix(1000, 0).UTC(), stop), c
}

// do sends one request with the given Host to h and returns the recorded
// answer.
func do(h http.Handler, method, path, host string, header map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	req.Host = host
	for k, v := range header {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestParseServeArgs(t *testing.T) {
	good := []struct {
		args []string
		want int
	}{
		{nil, DefaultPort},
		{[]string{"--port", "8080"}, 8080},
		{[]string{"--port=1024"}, 1024},
		{[]string{"-port", "65535"}, 65535},
	}
	for _, tc := range good {
		got, err := ParseServeArgs(tc.args)
		if err != nil || got != tc.want {
			t.Errorf("ParseServeArgs(%q) = %d, %v; want %d, nil", tc.args, got, err, tc.want)
		}
	}

	bad := [][]string{
		{"--port", "1023"},
		{"--port", "65536"},
		{"--port", "0"},
		{"--port", "-1"},
		{"--port", "abc"},
		{"--port", "80.5"},
		{"--port="},
	}
	for _, args := range bad {
		_, err := ParseServeArgs(args)
		if err == nil || err.Error() != "sdlc dashboard: --port must be 1024-65535" {
			t.Errorf("ParseServeArgs(%q) error = %v; want the exact port error", args, err)
		}
	}

	for _, args := range [][]string{{"--nope"}, {"extra"}} {
		_, err := ParseServeArgs(args)
		if err == nil || err.Error() != UsageError {
			t.Errorf("ParseServeArgs(%q) error = %v; want %q", args, err, UsageError)
		}
	}
}

func TestServe_BadPortExits2WithoutListen(t *testing.T) {
	isolateCacheDir(t)
	for _, port := range []int{0, 80, 1023, 65536} {
		errOut := captureStderr(t)
		o := testOptions(t, &fakeCollector{})
		o.Port = port
		if got := Serve(context.Background(), o); got != 2 {
			t.Errorf("Serve(port %d) = %d; want 2", port, got)
		}
		if got := errOut.String(); got != "sdlc dashboard: --port must be 1024-65535\n" {
			t.Errorf("stderr = %q; want the exact port error line", got)
		}
	}
	if _, err := dashboard.ReadServerRecord(); !errors.Is(err, dashboard.ErrNoServer) {
		t.Errorf("server.json written after a bad port: %v", err)
	}
}

func TestServe_BusyPort(t *testing.T) {
	inUse := &net.OpError{Op: "listen", Net: "tcp", Err: os.NewSyscallError("bind", syscall.EADDRINUSE)}
	cases := []struct {
		name      string
		healthErr error
		want      int
	}{
		{"sdlc server answers", nil, 0},
		{"another program holds the port", errors.New("connection refused"), 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			isolateCacheDir(t)
			errOut := captureStderr(t)
			o := testOptions(t, &fakeCollector{})
			var healthPort int
			o.Listen = func(network, addr string) (net.Listener, error) {
				if network != "tcp" || addr != testHost {
					t.Errorf("Listen(%q, %q); want tcp, %s", network, addr, testHost)
				}
				return nil, inUse
			}
			o.Health = func(port int, _ time.Duration) (dashboard.Health, error) {
				healthPort = port
				if tc.healthErr != nil {
					return dashboard.Health{}, tc.healthErr
				}
				return dashboard.Health{PID: 99, Version: "v-other"}, nil
			}
			if got := Serve(context.Background(), o); got != tc.want {
				t.Fatalf("Serve = %d; want %d", got, tc.want)
			}
			if healthPort != testPort {
				t.Errorf("Health port = %d; want %d", healthPort, testPort)
			}
			if tc.want == 3 && !strings.Contains(errOut.String(), "in use by another program") {
				t.Errorf("stderr = %q; want an in-use line", errOut.String())
			}
			if _, err := dashboard.ReadServerRecord(); !errors.Is(err, dashboard.ErrNoServer) {
				t.Errorf("server.json written on a busy port: %v", err)
			}
		})
	}
}

func TestHandler_HostCheck(t *testing.T) {
	h, _ := newTestHandler(t, nil)
	paths := []struct{ method, path string }{
		{"GET", "/"},
		{"GET", "/static/app.css"},
		{"GET", "/api/snapshot"},
		{"GET", "/api/health"},
		{"POST", "/api/stop"},
		{"GET", "/nope"},
	}
	for _, host := range []string{"evil.example:7385", "127.0.0.1:9999", "localhost", "127.0.0.1", ""} {
		for _, p := range paths {
			rec := do(h, p.method, p.path, host, map[string]string{"Origin": testOrigin, tokenHeader: testToken})
			if rec.Code != http.StatusForbidden {
				t.Errorf("%s %s Host %q = %d; want 403", p.method, p.path, host, rec.Code)
			}
		}
	}
	for _, host := range []string{"127.0.0.1:7385", "localhost:7385"} {
		if rec := do(h, "GET", "/api/health", host, nil); rec.Code != http.StatusOK {
			t.Errorf("GET /api/health Host %q = %d; want 200", host, rec.Code)
		}
	}
}

func TestHandler_Routes(t *testing.T) {
	h, _ := newTestHandler(t, nil)
	cases := []struct {
		method, path string
		want         int
	}{
		{"GET", "/", 200},
		{"GET", "/static/app.css", 200},
		{"GET", "/static/app.js", 200},
		{"GET", "/static/view.js", 200},
		{"GET", "/static/render.js", 200},
		{"GET", "/static/fonts/BarlowSemiCondensed-SemiBold.woff2", 200},
		{"GET", "/api/snapshot", 200},
		{"GET", "/api/health", 200},
		{"GET", "/static/missing.js", 404},
		{"GET", "/static/", 404},
		{"GET", "/static/fonts/", 404},
		{"GET", "/nope", 404},
		{"GET", "/api/nope", 404},
		{"POST", "/", 405},
		{"POST", "/api/snapshot", 405},
		{"POST", "/api/events", 405},
		{"DELETE", "/api/health", 405},
		{"GET", "/api/stop", 405},
		{"PUT", "/api/stop", 405},
	}
	for _, tc := range cases {
		if rec := do(h, tc.method, tc.path, testHost, nil); rec.Code != tc.want {
			t.Errorf("%s %s = %d; want %d", tc.method, tc.path, rec.Code, tc.want)
		}
	}
}

func TestHandler_HealthAndSnapshot(t *testing.T) {
	h, _ := newTestHandler(t, nil)

	rec := do(h, "GET", "/api/health", testHost, nil)
	var health dashboard.Health
	if err := json.Unmarshal(rec.Body.Bytes(), &health); err != nil {
		t.Fatalf("health body %q: %v", rec.Body.String(), err)
	}
	want := dashboard.Health{PID: 4242, Version: testVersion, StartedAt: time.Unix(1000, 0).UTC()}
	if !health.StartedAt.Equal(want.StartedAt) || health.PID != want.PID || health.Version != want.Version {
		t.Errorf("health = %+v; want %+v", health, want)
	}

	rec = do(h, "GET", "/api/snapshot", testHost, nil)
	var snap tools.DashboardSnapshot
	if err := json.Unmarshal(rec.Body.Bytes(), &snap); err != nil {
		t.Fatalf("snapshot body %q: %v", rec.Body.String(), err)
	}
	if len(snap.Repos) != 1 || snap.Repos[0].Root != "/repo/a" || snap.Version != testVersion {
		t.Errorf("snapshot = %+v; want one repo /repo/a of %s", snap, testVersion)
	}
}

func TestNewToken(t *testing.T) {
	a, b := NewToken(), NewToken()
	for _, tok := range []string{a, b} {
		raw, err := hex.DecodeString(tok)
		if err != nil || len(raw) != 32 || len(tok) != 64 {
			t.Errorf("NewToken() = %q; want 64 hex chars (32 bytes)", tok)
		}
	}
	if a == b {
		t.Error("two NewToken calls returned the same token")
	}
}

func TestHandler_TokenOnlyInIndex(t *testing.T) {
	h, _ := newTestHandler(t, nil)

	rec := do(h, "GET", "/", testHost, nil)
	body := rec.Body.String()
	if !strings.Contains(body, `<meta name="sdlc-token" content="`+testToken+`">`) {
		t.Errorf("GET / has no token meta tag:\n%s", body)
	}
	if strings.Contains(body, tokenPlaceholder) {
		t.Error("GET / still holds the token placeholder")
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("GET / Cache-Control = %q; want no-store", got)
	}

	others := []struct{ method, path, host string }{
		{"GET", "/static/app.js", testHost},
		{"GET", "/static/view.js", testHost},
		{"GET", "/static/render.js", testHost},
		{"GET", "/static/index.html", testHost},
		{"GET", "/api/snapshot", testHost},
		{"GET", "/api/health", testHost},
		{"GET", "/nope", testHost},
		{"GET", "/api/stop", testHost},
		{"POST", "/api/stop", testHost},
		{"GET", "/", "evil.example:7385"},
	}
	for _, o := range others {
		rec := do(h, o.method, o.path, o.host, nil)
		if strings.Contains(rec.Body.String(), testToken) {
			t.Errorf("%s %s (Host %s) body holds the token", o.method, o.path, o.host)
		}
		for k, vs := range rec.Header() {
			for _, v := range vs {
				if strings.Contains(v, testToken) {
					t.Errorf("%s %s header %s holds the token", o.method, o.path, k)
				}
			}
		}
	}
}

func TestHandler_StopChecks(t *testing.T) {
	cases := []struct {
		name   string
		header map[string]string
		want   int
	}{
		{"good origin and token", map[string]string{"Origin": testOrigin, tokenHeader: testToken}, 202},
		{"localhost origin", map[string]string{"Origin": "http://localhost:7385", tokenHeader: testToken}, 202},
		{"missing origin", map[string]string{tokenHeader: testToken}, 403},
		{"foreign origin", map[string]string{"Origin": "http://evil.example", tokenHeader: testToken}, 403},
		{"origin of another port", map[string]string{"Origin": "http://127.0.0.1:9999", tokenHeader: testToken}, 403},
		{"https origin", map[string]string{"Origin": "https://127.0.0.1:7385", tokenHeader: testToken}, 403},
		{"missing token", map[string]string{"Origin": testOrigin}, 403},
		{"wrong token", map[string]string{"Origin": testOrigin, tokenHeader: strings.Repeat("f", 64)}, 403},
		{"token prefix", map[string]string{"Origin": testOrigin, tokenHeader: testToken[:63]}, 403},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var stops int
			h, _ := newTestHandler(t, func() { stops++ })
			rec := do(h, "POST", "/api/stop", testHost, tc.header)
			if rec.Code != tc.want {
				t.Fatalf("POST /api/stop = %d; want %d", rec.Code, tc.want)
			}
			wantStops := 0
			if tc.want == 202 {
				wantStops = 1
				if got := strings.TrimSpace(rec.Body.String()); got != `{"stopping":true}` {
					t.Errorf("body = %q; want {\"stopping\":true}", got)
				}
			}
			if stops != wantStops {
				t.Errorf("stop calls = %d; want %d", stops, wantStops)
			}
		})
	}
}

// TestStopUsesConstantTimeCompare is a tripwire: the stop token compare must
// stay constant-time, so its timing tells nothing about the token.
func TestStopUsesConstantTimeCompare(t *testing.T) {
	src, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), "subtle.ConstantTimeCompare([]byte(r.Header.Get(tokenHeader)), []byte(h.token))") {
		t.Error("serveStop no longer compares the token with crypto/subtle.ConstantTimeCompare")
	}
}

// runEvents runs GET /api/events on h until stopWhen returns, then cancels
// the request and returns the stream body.
func runEvents(t *testing.T, h http.Handler, stopWhen func()) string {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest("GET", "/api/events", nil).WithContext(ctx)
	req.Host = testHost
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.ServeHTTP(rec, req)
	}()
	stopWhen()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("events handler did not return after the request ended")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/events = %d; want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Errorf("Content-Type = %q; want text/event-stream", got)
	}
	return rec.Body.String()
}

// waitFor polls cond every 2ms for up to 2s.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timeout waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// snapshotEvents returns the data of every "snapshot" event in an SSE body.
func snapshotEvents(t *testing.T, body string) []tools.DashboardSnapshot {
	t.Helper()
	var out []tools.DashboardSnapshot
	for _, block := range strings.Split(body, "\n\n") {
		if !strings.HasPrefix(block, "event: snapshot\ndata: ") {
			continue
		}
		var s tools.DashboardSnapshot
		if err := json.Unmarshal([]byte(strings.TrimPrefix(block, "event: snapshot\ndata: ")), &s); err != nil {
			t.Fatalf("snapshot event data: %v", err)
		}
		out = append(out, s)
	}
	return out
}

func TestEvents_SnapshotOnlyOnHashChange(t *testing.T) {
	if pingInterval != 15*time.Second {
		t.Errorf("pingInterval = %v; want 15s", pingInterval)
	}
	shortTimings(t, 2*time.Millisecond, time.Hour)
	h, c := newTestHandler(t, nil)

	body := runEvents(t, h, func() {
		// Many polls with the same data (GeneratedAt still changes).
		waitFor(t, "10 collects", func() bool { return c.callCount() >= 10 })
		c.setRepo("b")
		n := c.callCount()
		waitFor(t, "collects after the change", func() bool { return c.callCount() >= n+5 })
	})

	if !strings.HasPrefix(body, "retry: 3000\n\nevent: snapshot\ndata: ") {
		t.Errorf("stream does not start with retry then a snapshot event:\n%.200s", body)
	}
	events := snapshotEvents(t, body)
	if len(events) != 2 {
		t.Fatalf("got %d snapshot events; want 2 (connect, one change)", len(events))
	}
	if events[0].Repos[0].Name != "a" || events[1].Repos[0].Name != "b" {
		t.Errorf("event repos = %q, %q; want a, b", events[0].Repos[0].Name, events[1].Repos[0].Name)
	}
	if strings.Contains(body, testToken) {
		t.Error("event stream holds the token")
	}
}

func TestEvents_Ping(t *testing.T) {
	shortTimings(t, time.Hour, 5*time.Millisecond)
	h, _ := newTestHandler(t, nil)
	body := runEvents(t, h, func() { time.Sleep(40 * time.Millisecond) })
	if !strings.Contains(body, "\n\n: ping\n\n") {
		t.Errorf("no ping comment in stream:\n%s", body)
	}
	if n := len(snapshotEvents(t, body)); n != 1 {
		t.Errorf("got %d snapshot events; want 1", n)
	}
}

// pipeListener is an in-memory net.Listener: client connections are
// net.Pipe pairs, so Serve tests bind no real port.
type pipeListener struct {
	conns  chan net.Conn
	closed chan struct{}
	once   sync.Once
}

type pipeAddr struct{}

func (pipeAddr) Network() string { return "pipe" }
func (pipeAddr) String() string  { return testHost }

func newPipeListener() *pipeListener {
	return &pipeListener{conns: make(chan net.Conn), closed: make(chan struct{})}
}

func (l *pipeListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *pipeListener) Close() error {
	l.once.Do(func() { close(l.closed) })
	return nil
}

func (l *pipeListener) Addr() net.Addr { return pipeAddr{} }

// client returns an HTTP client whose every connection goes to l.
func (l *pipeListener) client() *http.Client {
	return &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			DisableKeepAlives: true,
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				server, client := net.Pipe()
				select {
				case l.conns <- server:
					return client, nil
				case <-l.closed:
				case <-ctx.Done():
				}
				server.Close()
				client.Close()
				return nil, net.ErrClosed
			},
		},
	}
}

// running is a Serve call under test.
type running struct {
	client *http.Client
	done   chan int
	stops  *atomic.Int32
}

// startServe runs Serve with o in the background on an in-memory listener
// and waits until it answers health.
func startServe(t *testing.T, o Options) *running {
	t.Helper()
	l := newPipeListener()
	o.Listen = func(network, addr string) (net.Listener, error) {
		if network != "tcp" || addr != testHost {
			t.Errorf("Listen(%q, %q); want tcp, %s", network, addr, testHost)
		}
		return l, nil
	}
	stops := &atomic.Int32{}
	o.Stop = func() { stops.Add(1) }
	ctx, cancel := context.WithCancel(context.Background())
	r := &running{client: l.client(), done: make(chan int, 1), stops: stops}
	go func() { r.done <- Serve(ctx, o) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-r.done:
		case <-time.After(3 * time.Second):
			t.Error("Serve did not return at cleanup")
		}
	})
	if code := r.get(t, "/api/health"); code != 200 {
		t.Fatalf("GET /api/health = %d; want 200", code)
	}
	return r
}

func (r *running) get(t *testing.T, path string) int {
	t.Helper()
	resp, err := r.client.Get("http://" + testHost + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return resp.StatusCode
}

func (r *running) postStop(t *testing.T, header map[string]string) int {
	t.Helper()
	req, err := http.NewRequest("POST", "http://"+testHost+"/api/stop", nil)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := r.client.Do(req)
	if err != nil {
		t.Fatalf("POST /api/stop: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return resp.StatusCode
}

// waitExit waits up to 2s for Serve to return and returns its code.
func (r *running) waitExit(t *testing.T) int {
	t.Helper()
	select {
	case code := <-r.done:
		r.done <- code // keep it for the cleanup
		return code
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not stop within 2s")
		return -1
	}
}

func (r *running) exited() bool {
	select {
	case code := <-r.done:
		r.done <- code
		return true
	default:
		return false
	}
}

func readRecordPID(t *testing.T) int {
	t.Helper()
	rec, err := dashboard.ReadServerRecord()
	if err != nil {
		t.Fatalf("ReadServerRecord: %v", err)
	}
	return rec.PID
}

func TestServe_WritesServerRecord(t *testing.T) {
	isolateCacheDir(t)
	startServe(t, testOptions(t, &fakeCollector{}))
	rec, err := dashboard.ReadServerRecord()
	if err != nil {
		t.Fatalf("ReadServerRecord: %v", err)
	}
	if rec.PID != os.Getpid() || rec.Port != testPort || rec.Version != testVersion || rec.URL != "http://127.0.0.1:7385" {
		t.Errorf("server record = %+v", rec)
	}
}

func TestServe_StopRequest(t *testing.T) {
	isolateCacheDir(t)
	r := startServe(t, testOptions(t, &fakeCollector{}))
	if pid := readRecordPID(t); pid != os.Getpid() {
		t.Fatalf("server.json pid = %d; want %d", pid, os.Getpid())
	}

	if code := r.postStop(t, map[string]string{"Origin": testOrigin, tokenHeader: testToken}); code != 202 {
		t.Fatalf("POST /api/stop = %d; want 202", code)
	}
	if code := r.waitExit(t); code != 0 {
		t.Errorf("Serve = %d; want 0", code)
	}
	if n := r.stops.Load(); n != 1 {
		t.Errorf("Stop calls = %d; want 1", n)
	}
	if _, err := dashboard.ReadServerRecord(); !errors.Is(err, dashboard.ErrNoServer) {
		t.Errorf("server.json still present after stop: %v", err)
	}
}

func TestServe_StopRejectedKeepsRunning(t *testing.T) {
	isolateCacheDir(t)
	r := startServe(t, testOptions(t, &fakeCollector{}))
	rejected := []map[string]string{
		{tokenHeader: testToken},
		{"Origin": "http://evil.example", tokenHeader: testToken},
		{"Origin": testOrigin},
		{"Origin": testOrigin, tokenHeader: "wrong"},
	}
	for _, header := range rejected {
		if code := r.postStop(t, header); code != 403 {
			t.Errorf("POST /api/stop %v = %d; want 403", header, code)
		}
	}
	time.Sleep(50 * time.Millisecond)
	if r.exited() {
		t.Fatal("Serve stopped after rejected stop requests")
	}
	if n := r.stops.Load(); n != 0 {
		t.Errorf("Stop calls = %d; want 0", n)
	}
	if code := r.get(t, "/api/health"); code != 200 {
		t.Errorf("GET /api/health = %d; want 200", code)
	}
	if pid := readRecordPID(t); pid != os.Getpid() {
		t.Errorf("server.json pid = %d; want %d", pid, os.Getpid())
	}
}

func TestServe_StopEndsOpenEventStream(t *testing.T) {
	isolateCacheDir(t)
	shortTimings(t, time.Hour, time.Hour)
	r := startServe(t, testOptions(t, &fakeCollector{repo: "a"}))

	resp, err := r.client.Get("http://" + testHost + "/api/events")
	if err != nil {
		t.Fatalf("GET /api/events: %v", err)
	}
	defer resp.Body.Close()
	stream := bufio.NewReader(resp.Body)
	line, err := stream.ReadString('\n')
	if err != nil || line != "retry: 3000\n" {
		t.Fatalf("first stream line = %q, %v; want retry: 3000", line, err)
	}
	// Keep reading like a browser does: net.Pipe has no buffer, so an unread
	// stream would block the server's last write.
	streamEnded := make(chan struct{})
	go func() {
		defer close(streamEnded)
		_, _ = io.Copy(io.Discard, stream)
	}()

	if code := r.postStop(t, map[string]string{"Origin": testOrigin, tokenHeader: testToken}); code != 202 {
		t.Fatalf("POST /api/stop = %d; want 202", code)
	}
	if code := r.waitExit(t); code != 0 {
		t.Errorf("Serve = %d; want 0", code)
	}
	select {
	case <-streamEnded:
	case <-time.After(2 * time.Second):
		t.Error("event stream still open after stop")
	}
}

// TestServe_NoIdleExit: with no client, no pipeline, and no session, a fake
// 24 h pass and the server still answers health — there is no idle timer.
func TestServe_NoIdleExit(t *testing.T) {
	isolateCacheDir(t)
	shortTimings(t, time.Millisecond, time.Millisecond)
	start := time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)
	var offset atomic.Int64
	oldNow := now
	now = func() time.Time { return start.Add(time.Duration(offset.Load())) }
	t.Cleanup(func() { now = oldNow })

	o := testOptions(t, &fakeCollector{})
	o.Roots = func(time.Time) ([]dashboard.Root, error) { return nil, nil }
	r := startServe(t, o)

	offset.Store(int64(24 * time.Hour))
	time.Sleep(50 * time.Millisecond)
	if r.exited() {
		t.Fatal("Serve exited with no client")
	}
	resp, err := r.client.Get("http://" + testHost + "/api/health")
	if err != nil {
		t.Fatalf("GET /api/health: %v", err)
	}
	defer resp.Body.Close()
	var h dashboard.Health
	if err := json.NewDecoder(resp.Body).Decode(&h); err != nil || resp.StatusCode != 200 {
		t.Fatalf("GET /api/health = %d, %v; want 200", resp.StatusCode, err)
	}
	if !h.StartedAt.Equal(start) {
		t.Errorf("startedAt = %v; want %v", h.StartedAt, start)
	}
}

func TestServe_Signals(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("signals to self are not supported on windows")
	}
	cases := []struct {
		name      string
		sig       syscall.Signal
		otherPID  bool
		wantGone  bool
		wantStops int32
	}{
		{"SIGTERM removes own record", syscall.SIGTERM, false, true, 0},
		{"SIGINT removes own record", syscall.SIGINT, false, true, 0},
		{"SIGTERM keeps a newer server's record", syscall.SIGTERM, true, false, 0},
		{"SIGINT keeps a newer server's record", syscall.SIGINT, true, false, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			isolateCacheDir(t)
			r := startServe(t, testOptions(t, &fakeCollector{}))
			otherPID := os.Getpid() + 100000
			if tc.otherPID {
				if err := dashboard.WriteServerRecord(dashboard.ServerRecord{PID: otherPID, Port: testPort}); err != nil {
					t.Fatal(err)
				}
			}
			self, err := os.FindProcess(os.Getpid())
			if err != nil {
				t.Fatal(err)
			}
			if err := self.Signal(tc.sig); err != nil {
				t.Fatal(err)
			}
			if code := r.waitExit(t); code != 0 {
				t.Errorf("Serve = %d; want 0", code)
			}
			_, err = dashboard.ReadServerRecord()
			gone := errors.Is(err, dashboard.ErrNoServer)
			if gone != tc.wantGone {
				t.Errorf("server.json gone = %v (%v); want %v", gone, err, tc.wantGone)
			}
			if !tc.wantGone && readRecordPID(t) != otherPID {
				t.Errorf("server.json pid changed; want %d", otherPID)
			}
			if n := r.stops.Load(); n != tc.wantStops {
				t.Errorf("Stop calls = %d; want %d", n, tc.wantStops)
			}
		})
	}
}

// githubURL is the one external link the page may hold.
const githubURL = "https://github.com/rnagrodzki/sdlc-plugin"

var (
	// htmlCommentRe matches an HTML comment.
	htmlCommentRe = regexp.MustCompile(`(?s)<!--.*?-->`)
	// htmlScriptRe matches a script element: group 1 holds its attributes, group 2 its body.
	htmlScriptRe = regexp.MustCompile(`(?is)<script\b([^>]*)>(.*?)</script\s*>`)
	// htmlTagRe matches an opening tag: group 1 holds the tag name, group 2 its attributes.
	htmlTagRe = regexp.MustCompile(`(?s)<([a-zA-Z][a-zA-Z0-9]*)\b([^>]*)>`)
	// htmlURLAttrRe matches a src or href attribute. Its value is in group 1, 2, or 3.
	htmlURLAttrRe = regexp.MustCompile(`(?is)\b(?:src|href)\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s>]+))`)
	// htmlQuotedRe matches a quoted attribute value.
	htmlQuotedRe = regexp.MustCompile(`"[^"]*"|'[^']*'`)
	// htmlStyleAttrRe matches an attribute named style.
	htmlStyleAttrRe = regexp.MustCompile(`(?i)(?:^|\s)style\s*=`)
	// htmlEventAttrRe matches an inline event handler attribute such as onclick.
	htmlEventAttrRe = regexp.MustCompile(`(?i)(?:^|\s)on[a-z]+\s*=`)
	// htmlIDRe matches an id attribute: group 1 holds the id.
	htmlIDRe = regexp.MustCompile(`\sid="([^"]+)"`)
	// htmlScriptSrcRe matches the src of a script element: group 1 holds the path.
	htmlScriptSrcRe = regexp.MustCompile(`<script\s+src="([^"]+)"`)
	// htmlExternalRe matches a URL with a scheme, or a protocol-relative URL.
	htmlExternalRe = regexp.MustCompile(`(?i)^(?:[a-z][a-z0-9+.-]*:|//)`)
)

// staticFile returns the text of one embedded page file, for example "app.css".
func staticFile(t *testing.T, name string) string {
	t.Helper()
	b, err := staticFS.ReadFile("static/" + name)
	if err != nil {
		t.Fatalf("read embedded static/%s: %v", name, err)
	}
	return string(b)
}

// htmlOpenTag returns the opening tag that holds id="<id>", or "" when the page has none.
func htmlOpenTag(page, id string) string {
	re := regexp.MustCompile(`<[^>]*\sid="` + regexp.QuoteMeta(id) + `"[^>]*>`)
	return re.FindString(page)
}

// TestStaticIndex_TitleAndSingleToken pins the page title and the single token placeholder.
func TestStaticIndex_TitleAndSingleToken(t *testing.T) {
	page := staticFile(t, "index.html")

	title := ""
	if m := regexp.MustCompile(`(?is)<title>(.*?)</title>`).FindStringSubmatch(page); m != nil {
		title = strings.TrimSpace(m[1])
	}
	if title != "sdlc signal room" {
		t.Errorf("index.html title = %q; want %q", title, "sdlc signal room")
	}
	if n := strings.Count(page, tokenPlaceholder); n != 1 {
		t.Errorf("index.html holds %q %d times; want 1", tokenPlaceholder, n)
	}
}

// TestStaticIndex_NoInlineCodeOrExternalURLs pins the rules that keep the page safe to serve:
// no inline script, style, or event handler, and no URL that leaves the server except the GitHub link.
func TestStaticIndex_NoInlineCodeOrExternalURLs(t *testing.T) {
	page := htmlCommentRe.ReplaceAllString(staticFile(t, "index.html"), "")

	for _, m := range htmlScriptRe.FindAllStringSubmatch(page, -1) {
		if !strings.Contains(m[1], "src=") {
			t.Errorf("index.html has a <script> with no src: %q", m[0])
		}
		if strings.TrimSpace(m[2]) != "" {
			t.Errorf("index.html has an inline <script> body: %q", m[2])
		}
	}
	if regexp.MustCompile(`(?i)<style\b`).MatchString(page) {
		t.Error("index.html has a <style> element")
	}
	for _, m := range htmlTagRe.FindAllStringSubmatch(page, -1) {
		attrs := htmlQuotedRe.ReplaceAllString(m[2], `""`)
		if htmlStyleAttrRe.MatchString(attrs) {
			t.Errorf("<%s> has a style attribute: %q", m[1], m[2])
		}
		if htmlEventAttrRe.MatchString(attrs) {
			t.Errorf("<%s> has an inline event handler: %q", m[1], m[2])
		}
	}

	github := 0
	for _, m := range htmlURLAttrRe.FindAllStringSubmatch(page, -1) {
		url := m[1] + m[2] + m[3]
		switch {
		case url == githubURL:
			github++
		case htmlExternalRe.MatchString(url):
			t.Errorf("index.html has the external URL %q", url)
		}
	}
	if github != 1 {
		t.Errorf("index.html links %s %d times; want 1", githubURL, github)
	}

	link := regexp.MustCompile(`(?is)<a\b([^>]*href="` + regexp.QuoteMeta(githubURL) + `"[^>]*)>(.*?)</a>`).FindStringSubmatch(page)
	if link == nil {
		t.Fatal("index.html has no <a> for the GitHub link")
	}
	for _, want := range []string{`rel="noopener noreferrer"`, `target="_blank"`} {
		if !strings.Contains(link[1], want) {
			t.Errorf("GitHub link attributes %q lack %s", link[1], want)
		}
	}
	if !strings.Contains(link[2], "GitHub") {
		t.Errorf("GitHub link text = %q; want it to name GitHub", link[2])
	}
}

// TestStaticIndex_IdsAndScripts pins the ids the page script builds into, the tab wiring, the
// removed rail, and the script order.
func TestStaticIndex_IdsAndScripts(t *testing.T) {
	page := htmlCommentRe.ReplaceAllString(staticFile(t, "index.html"), "")

	counts := map[string]int{}
	for _, m := range htmlIDRe.FindAllStringSubmatch(page, -1) {
		counts[m[1]]++
	}
	for _, id := range []string{
		"conn", "conn-text", "totals", "stop-btn", "stop-dialog", "stop-dialog-text", "stop-confirm", "stop-cancel",
		"tab-pipelines", "tab-activity", "tab-history", "n-pipelines", "n-activity", "n-history",
		"filter-chips", "toggle-all", "panel-pipelines", "panel-activity", "panel-history", "feed",
	} {
		if counts[id] != 1 {
			t.Errorf("index.html has id %q %d times; want 1", id, counts[id])
		}
	}
	for _, id := range []string{"groups", "detail", "detail-body", "detail-hint", "empty", "learnings", "deferred"} {
		if counts[id] != 0 {
			t.Errorf("index.html still has the old id %q", id)
		}
	}
	if strings.Contains(page, "<aside") {
		t.Error("index.html still has an <aside> rail")
	}

	if n := strings.Count(page, `role="tablist"`); n != 1 {
		t.Errorf("index.html has %d tablists; want 1", n)
	}
	for _, name := range []string{"pipelines", "activity", "history"} {
		tab, panel := htmlOpenTag(page, "tab-"+name), htmlOpenTag(page, "panel-"+name)
		if !strings.Contains(tab, `role="tab"`) || !strings.Contains(tab, `aria-controls="panel-`+name+`"`) {
			t.Errorf("tab %q = %s; want role=tab and aria-controls panel-%s", name, tab, name)
		}
		if !strings.Contains(panel, `role="tabpanel"`) || !strings.Contains(panel, `aria-labelledby="tab-`+name+`"`) {
			t.Errorf("panel %q = %s; want role=tabpanel and aria-labelledby tab-%s", name, panel, name)
		}
		if name != "pipelines" && !regexp.MustCompile(`\shidden[\s>]`).MatchString(panel) {
			t.Errorf("panel %q = %s; want it hidden at load", name, panel)
		}
	}

	var scripts []string
	for _, m := range htmlScriptSrcRe.FindAllStringSubmatch(page, -1) {
		scripts = append(scripts, m[1])
	}
	want := []string{"static/view.js", "static/render.js", "static/app.js"}
	if strings.Join(scripts, ",") != strings.Join(want, ",") {
		t.Errorf("script order = %v; want %v", scripts, want)
	}
}

// cssRule is one top-level block of a stylesheet: a style rule, or an at-rule such as @media.
type cssRule struct {
	Prelude string // the selector list or the at-rule header, with whitespace collapsed
	Body    string // the text between the braces; empty for a statement such as @import
}

// cssFlat is a style rule with the header of the at-rule around it ("" at the top level).
type cssFlat struct {
	At        string
	Selectors []string
	Decls     map[string]string
}

// cssWant is one expected declaration: the value of prop in the rule for sel must contain has.
type cssWant struct{ at, sel, prop, has string }

var (
	// cssCommentRe matches a CSS comment.
	cssCommentRe = regexp.MustCompile(`(?s)/\*.*?\*/`)
	// hexColorRe matches a #RRGGBB color.
	hexColorRe = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)
	// cssVarRe matches a value that is one var() reference: group 1 holds the name.
	cssVarRe = regexp.MustCompile(`^var\((--[a-z0-9-]+)\)$`)
)

// parseCSS removes the comments from src and returns its top-level rules in order.
// It fails the test when the braces do not balance.
func parseCSS(t *testing.T, src string) []cssRule {
	t.Helper()
	src = cssCommentRe.ReplaceAllString(src, "")
	var rules []cssRule
	depth, start, bodyStart := 0, 0, 0
	prelude := ""
	for i := 0; i < len(src); i++ {
		switch src[i] {
		case '{':
			if depth == 0 {
				prelude = strings.Join(strings.Fields(src[start:i]), " ")
				bodyStart = i + 1
			}
			depth++
		case '}':
			depth--
			if depth < 0 {
				t.Fatalf("css: unmatched } at byte %d", i)
			}
			if depth == 0 {
				rules = append(rules, cssRule{Prelude: prelude, Body: src[bodyStart:i]})
				start = i + 1
			}
		case ';':
			if depth == 0 {
				rules = append(rules, cssRule{Prelude: strings.Join(strings.Fields(src[start:i]), " ")})
				start = i + 1
			}
		}
	}
	if depth != 0 {
		t.Fatalf("css: %d unclosed {", depth)
	}
	return rules
}

// cssDecls parses the declarations of a rule body into property -> value.
// A later declaration of the same property wins, as in the cascade.
func cssDecls(body string) map[string]string {
	decls := map[string]string{}
	for _, part := range strings.Split(body, ";") {
		prop, val, ok := strings.Cut(part, ":")
		if !ok {
			continue
		}
		decls[strings.ToLower(strings.TrimSpace(prop))] = strings.Join(strings.Fields(val), " ")
	}
	return decls
}

// flattenCSS returns the style rules of rules, including the ones inside @media, @supports, and
// @container. Other at-rules, such as @font-face and @keyframes, are left out.
func flattenCSS(t *testing.T, rules []cssRule, at string) []cssFlat {
	t.Helper()
	var flat []cssFlat
	for _, r := range rules {
		switch {
		case strings.HasPrefix(r.Prelude, "@media"), strings.HasPrefix(r.Prelude, "@supports"), strings.HasPrefix(r.Prelude, "@container"):
			flat = append(flat, flattenCSS(t, parseCSS(t, r.Body), r.Prelude)...)
		case strings.HasPrefix(r.Prelude, "@"):
		default:
			var sels []string
			for _, s := range strings.Split(r.Prelude, ",") {
				sels = append(sels, strings.TrimSpace(s))
			}
			flat = append(flat, cssFlat{At: at, Selectors: sels, Decls: cssDecls(r.Body)})
		}
	}
	return flat
}

// cssLookup merges the declarations of every style rule that lists sel, inside the at-rule with
// the header at ("" is the top level). It fails the test when no rule matches.
func cssLookup(t *testing.T, rules []cssRule, at, sel string) map[string]string {
	t.Helper()
	merged := map[string]string{}
	found := false
	for _, r := range flattenCSS(t, rules, "") {
		if r.At != at {
			continue
		}
		for _, s := range r.Selectors {
			if s == sel {
				found = true
				for k, v := range r.Decls {
					merged[k] = v
				}
			}
		}
	}
	if !found {
		t.Errorf("app.css has no rule for %q in %q", sel, at)
	}
	return merged
}

// luminance returns the WCAG relative luminance of a #RRGGBB color.
func luminance(t *testing.T, hex string) float64 {
	t.Helper()
	if !hexColorRe.MatchString(hex) {
		t.Fatalf("color %q is not #RRGGBB", hex)
	}
	channel := func(s string) float64 {
		v, err := strconv.ParseUint(s, 16, 8)
		if err != nil {
			t.Fatalf("color %q: %v", hex, err)
		}
		c := float64(v) / 255
		if c <= 0.03928 {
			return c / 12.92
		}
		return math.Pow((c+0.055)/1.055, 2.4)
	}
	return 0.2126*channel(hex[1:3]) + 0.7152*channel(hex[3:5]) + 0.0722*channel(hex[5:7])
}

// contrast returns the WCAG contrast ratio of two #RRGGBB colors.
func contrast(t *testing.T, a, b string) float64 {
	t.Helper()
	la, lb := luminance(t, a), luminance(t, b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// TestStaticCSS_TextContrast pins the contrast of every text color against --void, and pins that
// no color declaration uses a color outside that checked set (for example --rail-dim).
func TestStaticCSS_TextContrast(t *testing.T) {
	rules := parseCSS(t, staticFile(t, "app.css"))
	root := cssLookup(t, rules, "", ":root")
	void := root["--void"]

	checked := map[string]bool{}
	for _, name := range []string{"--ivory", "--rail", "--signal-green", "--signal-amber", "--signal-red", "--scan-cyan"} {
		checked[name] = true
		val := root[name]
		if val == "" {
			t.Errorf("app.css has no %s on :root", name)
			continue
		}
		if got := contrast(t, val, void); got < 4.5 {
			t.Errorf("%s %s on --void %s = %.2f:1; want 4.5:1 or more", name, val, void, got)
		}
	}

	for _, r := range flattenCSS(t, rules, "") {
		val, ok := r.Decls["color"]
		if !ok {
			continue
		}
		who := strings.Join(r.Selectors, ", ")
		switch {
		case val == "inherit" || val == "currentColor":
		case cssVarRe.MatchString(val):
			if name := cssVarRe.FindStringSubmatch(val)[1]; !checked[name] {
				t.Errorf("%s: color %s is not a checked text color", who, name)
			}
		case hexColorRe.MatchString(val):
			if got := contrast(t, val, void); got < 4.5 {
				t.Errorf("%s: color %s on --void = %.2f:1; want 4.5:1 or more", who, val, got)
			}
		default:
			t.Errorf("%s: color %q is not a checked text color", who, val)
		}
	}
}

// TestStaticCSS_FontAndPalette pins that the enamel palette is gone and that the font loads from
// the bundled file, not from a font installed on the machine.
func TestStaticCSS_FontAndPalette(t *testing.T) {
	css := staticFile(t, "app.css")
	if strings.Contains(css, "--enamel") {
		t.Error("app.css still uses --enamel")
	}
	if regexp.MustCompile(`(?i)@import|https?://`).MatchString(cssCommentRe.ReplaceAllString(css, "")) {
		t.Error("app.css loads something from outside the server")
	}

	var face map[string]string
	for _, r := range parseCSS(t, css) {
		if r.Prelude == "@font-face" {
			face = cssDecls(r.Body)
		}
	}
	if face == nil {
		t.Fatal("app.css has no @font-face")
	}
	if !strings.Contains(face["src"], `url("fonts/BarlowSemiCondensed-SemiBold.woff2")`) || strings.Contains(face["src"], "local(") {
		t.Errorf("@font-face src = %q; want the bundled url(...) file and no local()", face["src"])
	}
	if _, err := staticFS.ReadFile("static/fonts/BarlowSemiCondensed-SemiBold.woff2"); err != nil {
		t.Errorf("the bundled font file is not embedded: %v", err)
	}
}

// TestStaticCSS_LayoutRules pins the layout rules: a 56 px header, one scroll box (the stage), the
// block grid and tiles, the cursor of stations, and the rules for a narrow page.
func TestStaticCSS_LayoutRules(t *testing.T) {
	rules := parseCSS(t, staticFile(t, "app.css"))

	for _, w := range []cssWant{
		{"", "[hidden]", "display", "none"},
		{"", ".shell", "grid-template-rows", "56px"},
		{"", ".stage", "overflow-y", "auto"},
		{"", ".filter-bar", "position", "sticky"},
		{"", ".step-detail", "grid-template-columns", "repeat(auto-fill, minmax(min(100%, 400px), 1fr))"},
		{"", ".step-detail", "gap", "14px"},
		{"", ".step-sec.wide", "grid-column", "1 / -1"},
		{"@container (min-width: 830px)", ".step-sec.span2", "grid-column", "span 2"},
		{"", ".step-sec", "padding", "12px 16px"},
		{"", ".step-sec", "border-radius", "8px"},
		{"", ".step-sec:not([open])", "align-self", "start"},
		{"", ".step-sec.selected", "border-color", "var(--scan-cyan-dim)"},
		{"", ".waves", "grid-template-columns", "minmax(min(100%, 320px), 1fr)"},
		{"", ".dim-cols", "grid-template-columns", "minmax(100px, 170px)"},
		{"", ".track", "max-width", "1100px"},
		{"", ".track", "grid-template-columns", "repeat(auto-fit,"},
		{"", ".act-grid", "grid-template-columns", "minmax(min(100%, 560px), 1fr)"},
		{"", ".hist-panel", "max-width", "1180px"},
		{"", ".station", "cursor", "default"},
		{"", ".station.has-section", "cursor", "pointer"},
		{"@media (max-width: 1000px)", ".counts", "display", "none"},
	} {
		got := cssLookup(t, rules, w.at, w.sel)[w.prop]
		if !strings.Contains(got, w.has) {
			t.Errorf("%s { %s } = %q; want it to contain %q", w.sel, w.prop, got, w.has)
		}
	}

	for _, r := range flattenCSS(t, rules, "") {
		who := strings.Join(r.Selectors, ", ")
		for _, s := range r.Selectors {
			if s == ".rail" || s == ".layout" {
				t.Errorf("app.css still styles the old layout selector %s", s)
			}
		}
		for _, prop := range []string{"overflow", "overflow-y"} {
			for _, v := range strings.Fields(r.Decls[prop]) {
				if (v == "auto" || v == "scroll") && who != ".stage" {
					t.Errorf("%s { %s: %s }: only .stage may be a scroll box", who, prop, v)
				}
			}
		}
		if v := r.Decls["max-height"]; strings.Contains(v, "vh") {
			t.Errorf("%s { max-height: %s }: a list must not have a viewport-high box", who, v)
		}
	}
}

// TestStaticCSS_MotionAndFallbacks pins that every animated rule has a reduced-motion override,
// and that the panels turn opaque when backdrop-filter is missing.
func TestStaticCSS_MotionAndFallbacks(t *testing.T) {
	rules := parseCSS(t, staticFile(t, "app.css"))
	const reduce = "@media (prefers-reduced-motion: reduce)"

	stopped := map[string]string{}
	var animated []string
	for _, r := range flattenCSS(t, rules, "") {
		anim, has := r.Decls["animation"]
		for _, sel := range r.Selectors {
			switch {
			case r.At == reduce:
				stopped[sel] = anim
			case has && anim != "none":
				animated = append(animated, sel)
			}
		}
	}
	if len(animated) == 0 {
		t.Fatal("app.css has no animated rule; this check would pass for nothing")
	}
	for _, sel := range animated {
		if stopped[sel] != "none" {
			t.Errorf("%s is animated and has no animation: none inside %s", sel, reduce)
		}
	}

	const noBackdrop = "@supports not (backdrop-filter: blur(1px))"
	for _, sel := range []string{".topbar", ".filter-bar", ".track-panel", ".list-panel"} {
		bg := cssLookup(t, rules, noBackdrop, sel)["background"]
		if bg == "" || strings.Contains(bg, "rgba(") {
			t.Errorf("%s background inside %s = %q; want an opaque color", sel, noBackdrop, bg)
		}
	}
}

// htmlSinkNames are the DOM properties and methods that turn a string into markup.
var htmlSinkNames = []string{"innerHTML", "outerHTML", "insertAdjacentHTML"}

// stripJSComments removes the // and /* */ comments from src and keeps everything else, including
// the text of strings. It is a small lexer for the page scripts. It does not read regular
// expression literals, so a regex that holds a quote can hide code. It returns an error when src
// ends inside a string or a block comment, which is the sign of such a misread.
func stripJSComments(src string) (string, error) {
	var out strings.Builder
	for i := 0; i < len(src); {
		c := src[i]
		switch {
		case c == '\'' || c == '"' || c == '`':
			j := i + 1
			for j < len(src) && src[j] != c {
				if src[j] == '\\' {
					j++
				} else if src[j] == '\n' && c != '`' {
					return "", fmt.Errorf("unterminated string at byte %d", i)
				}
				j++
			}
			if j >= len(src) {
				return "", fmt.Errorf("unterminated string at byte %d", i)
			}
			out.WriteString(src[i : j+1])
			i = j + 1
		case c == '/' && i+1 < len(src) && src[i+1] == '/':
			for i < len(src) && src[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < len(src) && src[i+1] == '*':
			end := strings.Index(src[i+2:], "*/")
			if end < 0 {
				return "", fmt.Errorf("unterminated block comment at byte %d", i)
			}
			i += end + 4
		default:
			out.WriteByte(c)
			i++
		}
	}
	return out.String(), nil
}

// findHTMLSinks returns the HTML string sinks that the code of src uses. Comments do not count.
func findHTMLSinks(src string) ([]string, error) {
	code, err := stripJSComments(src)
	if err != nil {
		return nil, err
	}
	var found []string
	for _, name := range htmlSinkNames {
		if strings.Contains(code, name) {
			found = append(found, name)
		}
	}
	return found, nil
}

// TestFindHTMLSinks pins how the sink check reads comments, strings, and code.
func TestFindHTMLSinks(t *testing.T) {
	cases := []struct {
		name, src string
		want      string
		wantErr   bool
	}{
		{"code", "el.innerHTML = x;", "innerHTML", false},
		{"outer", "el.outerHTML = x;", "outerHTML", false},
		{"insert", "el.insertAdjacentHTML('beforeend', x);", "insertAdjacentHTML", false},
		{"line comment", "// never innerHTML\nel.textContent = x;", "", false},
		{"block comment", "/* outerHTML */ el.textContent = x;", "", false},
		{"code after comment", "// ok\nel.innerHTML = x;", "innerHTML", false},
		{"comment marker in string", "var u = 'http://a'; el.innerHTML = x;", "innerHTML", false},
		{"sink in string", "var s = \"innerHTML\";", "innerHTML", false},
		{"escaped quote", `var s = 'it\'s // x'; el.innerHTML = x;`, "innerHTML", false},
		{"template", "var s = `a // b`; el.insertAdjacentHTML(x);", "insertAdjacentHTML", false},
		{"unterminated string", "var s = 'abc\nel.innerHTML = x;", "", true},
		{"unterminated block", "/* innerHTML", "", true},
	}
	for _, tc := range cases {
		got, err := findHTMLSinks(tc.src)
		if (err != nil) != tc.wantErr {
			t.Errorf("%s: error = %v; want error %v", tc.name, err, tc.wantErr)
			continue
		}
		if strings.Join(got, ",") != tc.want {
			t.Errorf("%s: sinks = %v; want %q", tc.name, got, tc.want)
		}
	}
}

// TestStaticJS_NoHTMLStringSinks pins that no page script turns a string into markup. Snapshot
// text must enter the page through textContent only.
func TestStaticJS_NoHTMLStringSinks(t *testing.T) {
	for _, name := range []string{"app.js", "view.js", "render.js"} {
		sinks, err := findHTMLSinks(staticFile(t, name))
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if len(sinks) > 0 {
			t.Errorf("%s uses %v; use textContent", name, sinks)
		}
	}
}

var (
	// jsPageGlobalRe matches the page globals document and window as whole words.
	jsPageGlobalRe = regexp.MustCompile(`\b(document|window)\b`)
	// jsByIDCallRe matches any call of byId.
	jsByIDCallRe = regexp.MustCompile(`\bbyId\s*\(`)
	// jsByIDDefRe matches the definition of byId.
	jsByIDDefRe = regexp.MustCompile(`\bfunction\s+byId\s*\(`)
	// jsByIDLiteralRe matches a byId call with one quoted id: group 1 or 2 holds the id.
	jsByIDLiteralRe = regexp.MustCompile(`\bbyId\(\s*(?:'([^']*)'|"([^"]*)")\s*\)`)
)

// findPageGlobals returns each use of document or window in the code of src. Comments do not count.
func findPageGlobals(src string) ([]string, error) {
	code, err := stripJSComments(src)
	if err != nil {
		return nil, err
	}
	return jsPageGlobalRe.FindAllString(code, -1), nil
}

// byIDCalls returns the ids of the byId calls in the code of src, and the number of calls whose
// argument is not one quoted string. Comments and the definition of byId do not count.
func byIDCalls(src string) (ids []string, dynamic int, err error) {
	code, err := stripJSComments(src)
	if err != nil {
		return nil, 0, err
	}
	for _, m := range jsByIDLiteralRe.FindAllStringSubmatch(code, -1) {
		ids = append(ids, m[1]+m[2])
	}
	calls := len(jsByIDCallRe.FindAllString(code, -1)) - len(jsByIDDefRe.FindAllString(code, -1))
	return ids, calls - len(ids), nil
}

// TestFindPageGlobals pins how the render.js global check reads comments, strings, and names.
func TestFindPageGlobals(t *testing.T) {
	cases := []struct {
		name, src string
		want      string
	}{
		{"document", "var a = document.title;", "document"},
		{"window", "window.x = 1;", "window"},
		{"both", "document.body; window.x;", "document,window"},
		{"line comment", "// document and window\nvar a = doc;", ""},
		{"block comment", "/* window */ doc.createElement('p');", ""},
		{"longer name", "doc.documentElement; windowSize;", ""},
		{"in a string", "var s = 'window';", "window"},
	}
	for _, tc := range cases {
		got, err := findPageGlobals(tc.src)
		if err != nil {
			t.Errorf("%s: error = %v", tc.name, err)
			continue
		}
		if strings.Join(got, ",") != tc.want {
			t.Errorf("%s: globals = %v; want %q", tc.name, got, tc.want)
		}
	}
}

// TestStaticRenderJS_NoPageGlobals pins that render.js reaches the page only through the doc
// argument of each builder, so the Node tests can pass a fake document.
func TestStaticRenderJS_NoPageGlobals(t *testing.T) {
	src := staticFile(t, "render.js")
	if !strings.Contains(src, "function el(doc,") {
		t.Fatal("render.js has no el(doc, ...) builder; the check reads the wrong file")
	}
	found, err := findPageGlobals(src)
	if err != nil {
		t.Fatalf("render.js: %v", err)
	}
	if len(found) > 0 {
		t.Errorf("render.js uses the page globals %v outside comments; take the document as the doc argument", found)
	}
}

// TestByIDCalls pins how the id check reads byId calls.
func TestByIDCalls(t *testing.T) {
	cases := []struct {
		name, src   string
		wantIDs     string
		wantDynamic int
	}{
		{"definition only", "function byId(id) { return document.getElementById(id); }", "", 0},
		{"literal", "byId('feed'); byId(\"conn\");", "feed,conn", 0},
		{"dynamic", "byId('tab-' + name);", "", 1},
		{"comment", "// byId('gone')\nbyId('feed');", "feed", 0},
	}
	for _, tc := range cases {
		ids, dynamic, err := byIDCalls(tc.src)
		if err != nil {
			t.Errorf("%s: error = %v", tc.name, err)
			continue
		}
		if strings.Join(ids, ",") != tc.wantIDs || dynamic != tc.wantDynamic {
			t.Errorf("%s: ids = %v, dynamic = %d; want %q, %d", tc.name, ids, dynamic, tc.wantIDs, tc.wantDynamic)
		}
	}
}

// TestStaticAppJS_ByIDsExistInIndex pins that every byId call in app.js names an id of index.html.
// Each call takes one quoted id, so the check sees every id the script reads.
func TestStaticAppJS_ByIDsExistInIndex(t *testing.T) {
	page := htmlCommentRe.ReplaceAllString(staticFile(t, "index.html"), "")
	pageIDs := map[string]bool{}
	for _, m := range htmlIDRe.FindAllStringSubmatch(page, -1) {
		pageIDs[m[1]] = true
	}

	ids, dynamic, err := byIDCalls(staticFile(t, "app.js"))
	if err != nil {
		t.Fatalf("app.js: %v", err)
	}
	if len(ids) == 0 {
		t.Fatal("app.js has no byId('<id>') call; the check reads the wrong file")
	}
	if dynamic > 0 {
		t.Errorf("app.js has %d byId call(s) without one quoted id; write each id as a literal", dynamic)
	}
	for _, id := range ids {
		if !pageIDs[id] {
			t.Errorf("app.js calls byId(%q), but index.html has no element with that id", id)
		}
	}
}
