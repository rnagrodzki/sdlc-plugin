package main

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/dashboard"
)

const (
	// proxySnapshotBody is the snapshot body the fake dashboard sends.
	proxySnapshotBody = `{"repos":[]}`
	// proxyReadOnlyBody is the full 405 body of a call that is not a GET.
	proxyReadOnlyBody = `{"error":{"code":"PREVIEW_READ_ONLY","message":"Disabled in the design preview. The draft does not change real runs.","suggestion":"Use the main dashboard for this action."}}`
	// proxyNotRunningBody is the full 502 body when the dashboard cannot be reached.
	proxyNotRunningBody = `{"error":{"code":"DASHBOARD_NOT_RUNNING","message":"Dashboard not running. Run /sdlc:dashboard, then reload.","suggestion":"Run /sdlc:dashboard, then reload."}}`
)

// proxyDashboard is a fake dashboard on 127.0.0.1. Like the real one, it
// refuses a Host that is not its own with 403.
type proxyDashboard struct {
	srv     *httptest.Server
	calls   atomic.Int32  // every request that reached the fake
	release chan struct{} // closing it lets /api/events send its second event

	mu    sync.Mutex
	hosts []string // the Host header of each request

	healthStatus int           // status of /api/health; 0 means 200
	healthDelay  time.Duration // wait before /api/health answers
}

// proxyFakeDashboard starts a fake dashboard and closes it at the end of the test.
// healthStatus 0 means 200.
func proxyFakeDashboard(t *testing.T, healthStatus int, healthDelay time.Duration) *proxyDashboard {
	t.Helper()
	d := &proxyDashboard{release: make(chan struct{}), healthStatus: healthStatus, healthDelay: healthDelay}
	d.srv = httptest.NewServer(http.HandlerFunc(d.serve))
	t.Cleanup(func() {
		d.releaseEvents()
		d.srv.Close()
	})

	return d
}

// releaseEvents closes the release channel once.
func (d *proxyDashboard) releaseEvents() {
	select {
	case <-d.release:
	default:
		close(d.release)
	}
}

// serve answers the fake dashboard routes.
func (d *proxyDashboard) serve(w http.ResponseWriter, r *http.Request) {
	d.calls.Add(1)
	d.mu.Lock()
	d.hosts = append(d.hosts, r.Host)
	d.mu.Unlock()

	if r.Host != d.srv.Listener.Addr().String() {
		http.Error(w, "forbidden host", http.StatusForbidden)
		return
	}
	switch r.URL.Path {
	case "/api/snapshot":
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, proxySnapshotBody)
	case "/api/events":
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: one\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-d.release:
		case <-r.Context().Done():
			return
		}
		_, _ = fmt.Fprint(w, "data: two\n\n")
	case "/api/health":
		select {
		case <-time.After(d.healthDelay):
		case <-r.Context().Done():
			return
		}
		if d.healthStatus != 0 {
			w.WriteHeader(d.healthStatus)
		}
		_, _ = fmt.Fprint(w, `{"pid":1}`)
	default:
		http.NotFound(w, r)
	}
}

// seenHosts returns the Host header of each request so far.
func (d *proxyDashboard) seenHosts() []string {
	d.mu.Lock()
	defer d.mu.Unlock()

	return append([]string(nil), d.hosts...)
}

// proxyRecord returns a ReadRecord function that gives a record with url.
func proxyRecord(url string) func() (dashboard.ServerRecord, error) {
	return func() (dashboard.ServerRecord, error) {
		return dashboard.ServerRecord{PID: 1, Port: 1, URL: url}, nil
	}
}

// proxyRecordErr returns a ReadRecord function that fails with err.
func proxyRecordErr(err error) func() (dashboard.ServerRecord, error) {
	return func() (dashboard.ServerRecord, error) {
		return dashboard.ServerRecord{}, err
	}
}

// proxyOptions returns the options for the test handlers. Port is serverPort.
func proxyOptions(read func() (dashboard.ServerRecord, error), stop func()) serverOptions {
	return serverOptions{DraftDir: "", Port: serverPort, ReadRecord: read, Stop: stop}
}

// proxyHandler builds the handler with the API routes and a health timeout.
func proxyHandler(read func() (dashboard.ServerRecord, error), timeout time.Duration) http.Handler {
	return newHandler(proxyOptions(read, func() {}), func(mux *http.ServeMux, o serverOptions) {
		registerAPIRoutes(mux, o, timeout)
	})
}

// proxyClosedURL returns the URL of a server that is already closed.
func proxyClosedURL(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()

	return url
}

// proxyAssertJSON checks status, body and the JSON headers of rec.
func proxyAssertJSON(t *testing.T, rec *httptest.ResponseRecorder, status int, body string) {
	t.Helper()
	serverAssertReply(t, rec, status, "application/json", body)
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
}

// proxyLivePreview serves the handler of build on a real 127.0.0.1 listener, so a client can read
// a stream while it flows. build gets the listener port. It returns the base URL.
func proxyLivePreview(t *testing.T, build func(port int) http.Handler) string {
	t.Helper()
	srv := httptest.NewUnstartedServer(nil)
	port := srv.Listener.Addr().(*net.TCPAddr).Port
	srv.Config.Handler = build(port)
	srv.Start()
	t.Cleanup(srv.Close)

	return srv.URL
}

func TestServerAPI(t *testing.T) {
	t.Run("GET /api/snapshot reaches the dashboard with its own Host", func(t *testing.T) {
		d := proxyFakeDashboard(t, 0, 0)
		h := proxyHandler(proxyRecord(d.srv.URL), time.Second)

		rec := serverGet(h, "/api/snapshot")

		serverAssertReply(t, rec, http.StatusOK, "application/json", proxySnapshotBody)
		hosts := d.seenHosts()
		if want := d.srv.Listener.Addr().String(); len(hosts) != 1 || hosts[0] != want {
			t.Errorf("dashboard saw hosts %q, want [%q]", hosts, want)
		}
	})

	t.Run("GET /api/events streams each event line", func(t *testing.T) {
		d := proxyFakeDashboard(t, 0, 0)
		base := proxyLivePreview(t, func(port int) http.Handler {
			o := proxyOptions(proxyRecord(d.srv.URL), func() {})
			o.Port = port
			return newHandler(o, registerAPI)
		})

		client := &http.Client{Timeout: 5 * time.Second}
		resp, err := client.Get(base + "/api/events")
		if err != nil {
			t.Fatalf("GET /api/events: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		if got := resp.Header.Get("Content-Type"); got != "text/event-stream" {
			t.Errorf("Content-Type = %q, want text/event-stream", got)
		}

		// The fake holds the second event until release closes. The first line
		// arrives before that only when the proxy flushes each write.
		lines := bufio.NewReader(resp.Body)
		first, err := lines.ReadString('\n')
		if err != nil || first != "data: one\n" {
			t.Fatalf("first line = %q, %v; want %q", first, err, "data: one\n")
		}
		d.releaseEvents()
		rest := ""
		for {
			line, err := lines.ReadString('\n')
			rest += line
			if err != nil {
				break
			}
		}
		if rest != "\ndata: two\n\n" {
			t.Errorf("rest of stream = %q, want %q", rest, "\ndata: two\n\n")
		}
		hosts := d.seenHosts()
		if want := d.srv.Listener.Addr().String(); len(hosts) != 1 || hosts[0] != want {
			t.Errorf("dashboard saw hosts %q, want [%q]", hosts, want)
		}
	})

	recordErrors := []struct {
		name string
		err  error
	}{
		{"ErrNoServer", fmt.Errorf("dashboard: server.json: %w", dashboard.ErrNoServer)},
		{"another record error", errors.New("dashboard: read server.json: permission denied")},
	}
	for _, tc := range recordErrors {
		t.Run("GET /api/ with "+tc.name+" gives 502", func(t *testing.T) {
			h := proxyHandler(proxyRecordErr(tc.err), time.Second)

			proxyAssertJSON(t, serverGet(h, "/api/snapshot"), http.StatusBadGateway, proxyNotRunningBody)
		})
	}

	t.Run("GET /api/ with a record URL without a host gives 502", func(t *testing.T) {
		h := proxyHandler(proxyRecord(""), time.Second)

		proxyAssertJSON(t, serverGet(h, "/api/snapshot"), http.StatusBadGateway, proxyNotRunningBody)
	})

	t.Run("GET /api/ with a closed dashboard gives 502", func(t *testing.T) {
		h := proxyHandler(proxyRecord(proxyClosedURL(t)), time.Second)

		proxyAssertJSON(t, serverGet(h, "/api/events"), http.StatusBadGateway, proxyNotRunningBody)
	})

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		t.Run(method+" /api/run-archive gives 405 and never reaches the dashboard", func(t *testing.T) {
			d := proxyFakeDashboard(t, 0, 0)
			h := proxyHandler(proxyRecord(d.srv.URL), time.Second)

			rec := serverDo(h, method, "/api/run-archive", serverHost)

			proxyAssertJSON(t, rec, http.StatusMethodNotAllowed, proxyReadOnlyBody)
			if n := d.calls.Load(); n != 0 {
				t.Errorf("dashboard saw %d requests, want 0", n)
			}
		})
	}

	t.Run("GET /__design/status is running with the dashboard", func(t *testing.T) {
		d := proxyFakeDashboard(t, 0, 0)
		h := newHandler(proxyOptions(proxyRecord(d.srv.URL), func() {}), registerAPI)

		rec := serverGet(h, "/__design/status")

		proxyAssertJSON(t, rec, http.StatusOK, `{"dashboard":"running","url":"`+d.srv.URL+`"}`)
	})

	t.Run("GET /__design/status is not running with no record", func(t *testing.T) {
		h := proxyHandler(proxyRecordErr(dashboard.ErrNoServer), time.Second)

		proxyAssertJSON(t, serverGet(h, "/__design/status"), http.StatusOK, `{"dashboard":"not running","url":""}`)
	})

	t.Run("GET /__design/status is not running when the health call fails", func(t *testing.T) {
		d := proxyFakeDashboard(t, http.StatusInternalServerError, 0)
		h := proxyHandler(proxyRecord(d.srv.URL), time.Second)

		proxyAssertJSON(t, serverGet(h, "/__design/status"), http.StatusOK, `{"dashboard":"not running","url":"`+d.srv.URL+`"}`)
	})

	t.Run("GET /__design/status is not running with a closed dashboard", func(t *testing.T) {
		url := proxyClosedURL(t)
		h := proxyHandler(proxyRecord(url), time.Second)

		proxyAssertJSON(t, serverGet(h, "/__design/status"), http.StatusOK, `{"dashboard":"not running","url":"`+url+`"}`)
	})

	t.Run("GET /__design/status is not running when the health call times out", func(t *testing.T) {
		d := proxyFakeDashboard(t, 0, 5*time.Second)
		h := proxyHandler(proxyRecord(d.srv.URL), 50*time.Millisecond)

		start := time.Now()
		rec := serverGet(h, "/__design/status")

		proxyAssertJSON(t, rec, http.StatusOK, `{"dashboard":"not running","url":"`+d.srv.URL+`"}`)
		if took := time.Since(start); took > 2*time.Second {
			t.Errorf("status took %v, want the 50ms timeout to end the health call", took)
		}
	})

	t.Run("POST /__design/stop gives 202 and calls Stop once", func(t *testing.T) {
		var calls atomic.Int32
		stopped := make(chan struct{}, 2)
		stop := func() {
			calls.Add(1)
			stopped <- struct{}{}
		}
		h := newHandler(proxyOptions(proxyRecordErr(dashboard.ErrNoServer), stop), registerAPI)

		rec := serverDo(h, http.MethodPost, "/__design/stop", serverHost)

		if rec.Code != http.StatusAccepted {
			t.Fatalf("status = %d, want 202", rec.Code)
		}
		if rec.Body.Len() != 0 {
			t.Errorf("body = %q, want empty", rec.Body.String())
		}
		if got := rec.Header().Get("Content-Type"); got != "" {
			t.Errorf("Content-Type = %q, want none", got)
		}
		select {
		case <-stopped:
		case <-time.After(3 * time.Second):
			t.Fatal("Stop was not called within 3s")
		}
		time.Sleep(50 * time.Millisecond)
		if n := calls.Load(); n != 1 {
			t.Errorf("Stop calls = %d, want 1", n)
		}
	})

	t.Run("the API routes keep the Host check", func(t *testing.T) {
		d := proxyFakeDashboard(t, 0, 0)
		h := proxyHandler(proxyRecord(d.srv.URL), time.Second)

		rec := serverDo(h, http.MethodGet, "/api/snapshot", "evil.example:4242")

		if rec.Code != http.StatusForbidden {
			t.Errorf("status = %d, want 403", rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "forbidden host") {
			t.Errorf("body = %q, want forbidden host", rec.Body.String())
		}
		if n := d.calls.Load(); n != 0 {
			t.Errorf("dashboard saw %d requests, want 0", n)
		}
	})
}
