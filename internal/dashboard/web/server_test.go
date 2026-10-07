package web

import (
	"bufio"
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
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
