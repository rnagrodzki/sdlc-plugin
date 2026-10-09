package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"
)

// statusTimeout is how long the status route waits for the dashboard health call.
const statusTimeout = 2 * time.Second

// Error codes of the API routes. Each error body is
// {"error":{"code","message","suggestion"}}, the shape of the dashboard API.
const (
	// codeReadOnly answers every API call that is not a GET.
	codeReadOnly = "PREVIEW_READ_ONLY"
	// codeNotRunning answers an API call when the dashboard cannot be reached.
	codeNotRunning = "DASHBOARD_NOT_RUNNING"
)

// Texts of the error bodies.
const (
	// readOnlyMessage is the message of the codeReadOnly body.
	readOnlyMessage = "Disabled in the design preview. The draft does not change real runs."
	// readOnlySuggestion is the suggestion of the codeReadOnly body.
	readOnlySuggestion = "Use the main dashboard for this action."
	// notRunningMessage is the message of the codeNotRunning body.
	notRunningMessage = "Dashboard not running. Run /sdlc:dashboard, then reload."
	// notRunningSuggestion is the suggestion of the codeNotRunning body.
	notRunningSuggestion = "Run /sdlc:dashboard, then reload."
)

// Values of the dashboard field in the status reply.
const (
	// statusRunning means the health call returned 200.
	statusRunning = "running"
	// statusNotRunning means no record, a failed health call or a timeout.
	statusNotRunning = "not running"
)

// designStatus is the reply of GET /__design/status.
type designStatus struct {
	Dashboard string `json:"dashboard"` // statusRunning or statusNotRunning
	URL       string `json:"url"`       // the record URL; empty when there is no record
}

// registerAPI adds the API proxy, the status route and the stop route to mux.
// newHandler(o, registerAPI) wraps them in the Host check.
func registerAPI(mux *http.ServeMux, o serverOptions) {
	registerAPIRoutes(mux, o, statusTimeout)
}

// registerAPIRoutes is registerAPI with the health call timeout as a value, so
// the tests can use a short one.
func registerAPIRoutes(mux *http.ServeMux, o serverOptions, timeout time.Duration) {
	mux.HandleFunc("GET /api/", proxyToDashboard(o))
	// The GET pattern is more specific, so this pattern gets every other method.
	mux.HandleFunc("/api/", func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusMethodNotAllowed, codeReadOnly, readOnlyMessage, readOnlySuggestion)
	})
	mux.HandleFunc("GET /__design/status", serveStatus(o, timeout))
	mux.HandleFunc("POST /__design/stop", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		go o.Stop()
	})
}

// proxyToDashboard sends each GET request to the dashboard that the server
// record names. The record is read for each request, so a dashboard that
// starts or moves after the preview starts is found.
func proxyToDashboard(o serverOptions) http.HandlerFunc {
	notRunning := func(w http.ResponseWriter, _ *http.Request, _ error) {
		writeError(w, http.StatusBadGateway, codeNotRunning, notRunningMessage, notRunningSuggestion)
	}

	return func(w http.ResponseWriter, r *http.Request) {
		target, err := dashboardURL(o)
		if err != nil {
			notRunning(w, r, err)
			return
		}
		proxy := &httputil.ReverseProxy{
			// SetURL also sets the outbound Host to the dashboard host. The
			// dashboard refuses a Host that is not its own.
			Rewrite:       func(pr *httputil.ProxyRequest) { pr.SetURL(target) },
			FlushInterval: -1, // flush each write, so the event stream flows
			ErrorHandler:  notRunning,
		}
		proxy.ServeHTTP(w, r)
	}
}

// dashboardURL reads the server record and parses its URL.
func dashboardURL(o serverOptions) (*url.URL, error) {
	rec, err := o.ReadRecord()
	if err != nil {
		return nil, err
	}
	u, err := url.Parse(rec.URL)
	if err != nil {
		return nil, err
	}
	if u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("dashboard record URL %q has no scheme or host", rec.URL)
	}

	return u, nil
}

// serveStatus answers with the dashboard state from a health call that ends
// after timeout. A record error, a health error, a status other than 200 or a
// timeout gives statusNotRunning.
func serveStatus(o serverOptions, timeout time.Duration) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		reply := designStatus{Dashboard: statusNotRunning}
		rec, err := o.ReadRecord()
		if err == nil {
			reply.URL = rec.URL
			if dashboardHealthy(r.Context(), rec.URL, timeout) {
				reply.Dashboard = statusRunning
			}
		}
		writeJSONReply(w, http.StatusOK, reply)
	}
}

// dashboardHealthy reports whether GET <base>/api/health returns 200 within timeout.
func dashboardHealthy(ctx context.Context, base string, timeout time.Duration) bool {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/health", nil)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))

	return resp.StatusCode == http.StatusOK
}

// writeError writes {"error":{"code","message","suggestion"}} with status.
func writeError(w http.ResponseWriter, status int, code, message, suggestion string) {
	type apiError struct {
		Code       string `json:"code"`
		Message    string `json:"message"`
		Suggestion string `json:"suggestion"`
	}
	writeJSONReply(w, status, map[string]apiError{"error": {Code: code, Message: message, Suggestion: suggestion}})
}

// writeJSONReply writes v as JSON with status and the no-store cache header.
func writeJSONReply(w http.ResponseWriter, status int, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(data)
}
