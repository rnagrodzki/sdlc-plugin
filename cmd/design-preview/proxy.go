package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/dashboard"
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
	// notRunningMessage starts the message of the codeNotRunning body. The
	// cause follows it.
	notRunningMessage = "Dashboard not running"
	// notRunningSuggestion is the suggestion of the codeNotRunning body.
	notRunningSuggestion = "Run /sdlc:dashboard, then reload."
)

// Values of the dashboard field in the status reply.
const (
	// statusRunning means the health call returned an sdlc health answer.
	statusRunning = "running"
	// statusNotRunning means no record, a bad record URL, a failed health
	// call, an answer that is not an sdlc health answer, or a timeout.
	statusNotRunning = "not running"
)

// dashboardHealth is the health call of the status route: the dashboard
// package's own check, which also tells a foreign program on the port from
// the dashboard. Tests use real servers on 127.0.0.1, so it is not replaced.
var dashboardHealth = dashboard.DefaultDeps().Health

// designStatus is the reply of GET /__design/status.
type designStatus struct {
	Dashboard string `json:"dashboard"`        // statusRunning or statusNotRunning
	URL       string `json:"url"`              // the record URL; empty when there is no record
	Reason    string `json:"reason,omitempty"` // why the dashboard is not running; empty when it runs
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
// starts or moves after the preview starts is found. When the dashboard
// cannot be reached, the 502 message names the cause.
func proxyToDashboard(o serverOptions) http.HandlerFunc {
	notRunning := func(w http.ResponseWriter, _ *http.Request, err error) {
		writeError(w, http.StatusBadGateway, codeNotRunning, fmt.Sprintf("%s: %v", notRunningMessage, err), notRunningSuggestion)
	}

	return func(w http.ResponseWriter, r *http.Request) {
		rec, err := o.ReadRecord()
		if err != nil {
			notRunning(w, r, err)
			return
		}
		target, err := parseRecordURL(rec.URL)
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

// parseRecordURL parses the URL of the server record. A URL without a scheme
// or a host is an error.
func parseRecordURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	if u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("dashboard record URL %q has no scheme or host", raw)
	}

	return u, nil
}

// serveStatus answers with the dashboard state. The dashboard runs when the
// record reads, its URL parses, and dashboardHealth on the record port gives
// an sdlc health answer within timeout. Otherwise the reply is
// statusNotRunning and Reason holds the cause.
func serveStatus(o serverOptions, timeout time.Duration) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		reply := designStatus{Dashboard: statusNotRunning}
		rec, err := o.ReadRecord()
		if err == nil {
			reply.URL = rec.URL
			_, err = parseRecordURL(rec.URL)
		}
		if err == nil {
			_, err = dashboardHealth(rec.Port, timeout)
		}
		if err != nil {
			reply.Reason = err.Error()
		} else {
			reply.Dashboard = statusRunning
		}
		writeJSONReply(w, http.StatusOK, reply)
	}
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
