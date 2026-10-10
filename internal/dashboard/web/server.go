// Package web serves the local sdlc dashboard page and its JSON/SSE API on
// 127.0.0.1. It is started by "sdlc dashboard serve" (see cmd/sdlc) and is
// stopped by POST /api/stop, SIGTERM, or SIGINT — it has no idle timer.
package web

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/dashboard"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/tools"
)

const (
	// DefaultPort is the --port value when the flag is not given.
	DefaultPort = 7385
	minPort     = 1024
	maxPort     = 65535

	// PortError is the exact stderr line for a bad --port value.
	PortError = "sdlc dashboard: --port must be 1024-65535"
	// UsageError is the stderr line for a bad "sdlc dashboard" command line.
	UsageError = "usage: sdlc dashboard serve [--port N]"

	// tokenPlaceholder is the text in static/index.html that GET / replaces
	// with this server start's token.
	tokenPlaceholder = "{{SDLC_TOKEN}}"
	// tokenHeader carries the token on every state-changing request.
	tokenHeader = "X-Sdlc-Token"
	// maxMutationBody is the largest body guardMutation accepts.
	maxMutationBody = 8 << 10
	// sseRetryMillis is the reconnect delay the page's EventSource uses.
	sseRetryMillis = 3000
)

// Timings are package variables so tests can shorten them.
var (
	// snapshotPollInterval is how often /api/events collects a snapshot and
	// compares its hash with the last one sent.
	snapshotPollInterval = 2 * time.Second
	// pingInterval is how often /api/events sends an SSE comment line, so
	// proxies and the browser keep the stream open.
	pingInterval = 15 * time.Second
	// shutdownTimeout bounds the graceful shutdown; after it the server
	// closes every connection.
	shutdownTimeout = 1 * time.Second
	// healthProbeTimeout bounds the health call on a busy port.
	healthProbeTimeout = 1 * time.Second
	// now is the clock of the server (startedAt, snapshot time).
	now = time.Now
	// stderr receives the server's error lines.
	stderr io.Writer = os.Stderr
)

// Options configures Serve. Every function field is required except Stop,
// Archive, ClearCache, LearningBody, DeletePreplan, DeleteDeferred and
// DeleteLearning. A route whose function field is nil answers 500 with a
// suggestion instead of failing at start.
type Options struct {
	Port    int
	Version string
	// Token is 64 hex chars (see NewToken); tests pass a fixed value. An empty
	// Token makes Serve create one, so a stop request can never match "".
	Token string
	// Stop is called after POST /api/stop is accepted (production: cancels
	// the Serve context). Serve stops itself either way; tests record the call.
	Stop func()
	// Roots lists the registered repo roots (production: dashboard.Roots).
	Roots func(now time.Time) ([]dashboard.Root, error)
	// Collect builds the snapshot of roots (production: a closure over
	// tools.CollectDashboardSnapshot with the binary's version).
	Collect func(roots []string, now time.Time) tools.DashboardSnapshot
	// Listen opens the listener (production: net.Listen).
	Listen func(network, addr string) (net.Listener, error)
	// Health calls GET /api/health on 127.0.0.1:port (production:
	// dashboard.DefaultDeps().Health).
	Health func(port int, timeout time.Duration) (dashboard.Health, error)
	// Archive moves the files of one run into run-archive/ (production:
	// tools.ArchiveRun). POST /api/run-archive calls it.
	Archive func(in tools.ArchiveRunIn, now time.Time) (tools.ArchiveRunOut, error)
	// ClearCache deletes the regenerable cache files of one repo (production:
	// a closure over tools.ClearCache with the server log path).
	// POST /api/cache-clear calls it.
	ClearCache func(root string, now time.Time) (tools.ClearCacheOut, error)
	// LearningBody returns the text of one learning entry (production:
	// tools.DashboardLearningBody). GET /api/learning calls it.
	LearningBody func(root, date, heading string) (tools.DashboardLearningBodyOut, error)
	// DeletePreplan deletes one preplan topic file (production:
	// tools.DeletePreplanTopic). POST /api/preplan-delete calls it.
	DeletePreplan func(root, slug string) (tools.DeleteOut, error)
	// DeleteDeferred deletes one deferred item (production:
	// tools.DeleteDeferredItem). POST /api/deferred-delete calls it.
	DeleteDeferred func(root, id string) (tools.DeleteOut, error)
	// DeleteLearning deletes one learning entry (production:
	// tools.DeleteDashboardLearning). POST /api/learning-delete calls it.
	DeleteLearning func(root, date, heading string) (tools.DeleteOut, error)
}

// NewToken returns a new stop token: 32 bytes from crypto/rand, hex encoded.
func NewToken() string {
	b := make([]byte, 32)
	// crypto/rand.Read never returns an error since Go 1.24.
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// ParseServeArgs parses the flags of "sdlc dashboard serve" and returns the
// port. A bad --port value (not a number, or outside 1024-65535) returns an
// error whose text is PortError; any other bad flag returns UsageError.
func ParseServeArgs(args []string) (int, error) {
	fset := flag.NewFlagSet("serve", flag.ContinueOnError)
	fset.SetOutput(io.Discard)
	raw := fset.String("port", strconv.Itoa(DefaultPort), "")
	if err := fset.Parse(args); err != nil || fset.NArg() > 0 {
		return 0, errors.New(UsageError)
	}
	port, err := strconv.Atoi(*raw)
	if err != nil || !validPort(port) {
		return 0, errors.New(PortError)
	}
	return port, nil
}

func validPort(port int) bool {
	return port >= minPort && port <= maxPort
}

// Serve runs the dashboard server on 127.0.0.1:o.Port until ctx is
// cancelled, POST /api/stop is accepted, or the process gets SIGTERM or
// SIGINT. It writes the server record after the listener binds and removes
// it (only when it still names this process) at every stop.
//
// Exit codes: 0 stopped normally, or the port already answers sdlc health
// (another sdlc server runs there); 1 the HTTP server failed; 2 bad port;
// 3 the port is held by another program.
func Serve(ctx context.Context, o Options) int {
	if !validPort(o.Port) {
		fmt.Fprintln(stderr, PortError)
		return 2
	}
	token := o.Token
	if token == "" {
		token = NewToken()
	}

	ctx, stopSignals := signal.NotifyContext(ctx, syscall.SIGTERM, os.Interrupt)
	defer stopSignals()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(o.Port))
	ln, err := o.Listen("tcp", addr)
	if err != nil {
		// The usual cause is a busy port. When an sdlc server answers there,
		// the job is done: that server serves the dashboard.
		if _, herr := o.Health(o.Port, healthProbeTimeout); herr == nil {
			return 0
		}
		fmt.Fprintf(stderr, "sdlc dashboard: port %d is in use by another program: %v\n", o.Port, err)
		return 3
	}

	pid := os.Getpid()
	startedAt := now().UTC()
	rec := dashboard.ServerRecord{
		PID:       pid,
		Port:      o.Port,
		Version:   o.Version,
		StartedAt: startedAt,
		URL:       "http://" + addr,
	}
	if err := dashboard.WriteServerRecord(rec); err != nil {
		// The server still works; callers fall back to probing the port.
		fmt.Fprintf(stderr, "sdlc dashboard: %v\n", err)
	}
	defer func() { _ = dashboard.RemoveServerRecord(pid) }()

	stop := func() {
		if o.Stop != nil {
			o.Stop()
		}
		cancel()
	}
	srv := &http.Server{
		Handler:           newHandler(ctx, o, token, pid, startedAt, stop),
		ReadHeaderTimeout: 10 * time.Second,
		// Request contexts derive from ctx, so open event streams end at stop.
		BaseContext: func(net.Listener) context.Context { return ctx },
	}
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()

	code := 0
	select {
	case <-ctx.Done():
	case err := <-serveErr:
		fmt.Fprintf(stderr, "sdlc dashboard: %v\n", err)
		code = 1
	}

	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancelShutdown()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		_ = srv.Close()
	}
	return code
}

// handler serves every dashboard request. ServeHTTP checks the Host header
// first, then hands the request to mux.
type handler struct {
	ctx       context.Context
	o         Options
	token     string
	pid       int
	startedAt time.Time
	stop      func()
	index     string
	hosts     [2]string
	origins   [2]string
	mux       *http.ServeMux
}

func newHandler(ctx context.Context, o Options, token string, pid int, startedAt time.Time, stop func()) *handler {
	index, err := staticFS.ReadFile("static/index.html")
	if err != nil {
		// static/index.html is embedded at build time; a miss is a build defect.
		panic(fmt.Sprintf("dashboard web: embedded index.html: %v", err))
	}
	port := strconv.Itoa(o.Port)
	h := &handler{
		ctx:       ctx,
		o:         o,
		token:     token,
		pid:       pid,
		startedAt: startedAt,
		stop:      stop,
		index:     string(index),
		hosts:     [2]string{"127.0.0.1:" + port, "localhost:" + port},
		origins:   [2]string{"http://127.0.0.1:" + port, "http://localhost:" + port},
		mux:       http.NewServeMux(),
	}
	static, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic(fmt.Sprintf("dashboard web: embedded static dir: %v", err))
	}
	files := http.StripPrefix("/static", http.FileServerFS(static))

	h.mux.HandleFunc("GET /{$}", h.serveIndex)
	h.mux.HandleFunc("GET /static/", func(w http.ResponseWriter, r *http.Request) {
		// No directory listings.
		if strings.HasSuffix(r.URL.Path, "/") {
			http.NotFound(w, r)
			return
		}
		files.ServeHTTP(w, r)
	})
	h.mux.HandleFunc("GET /api/snapshot", h.serveSnapshot)
	h.mux.HandleFunc("GET /api/events", h.serveEvents)
	h.mux.HandleFunc("GET /api/health", h.serveHealth)
	h.mux.HandleFunc("GET /api/learning", h.serveLearning)
	h.mux.HandleFunc("POST /api/stop", h.serveStop)
	h.mux.HandleFunc("POST /api/run-archive", h.serveRunArchive)
	h.mux.HandleFunc("POST /api/cache-clear", h.serveCacheClear)
	h.mux.HandleFunc("POST /api/preplan-delete", h.servePreplanDelete)
	h.mux.HandleFunc("POST /api/deferred-delete", h.serveDeferredDelete)
	h.mux.HandleFunc("POST /api/learning-delete", h.serveLearningDelete)
	return h
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// A page on another site can reach 127.0.0.1 through DNS rebinding; its
	// requests carry that site's host name, so only loopback names pass.
	if r.Host != h.hosts[0] && r.Host != h.hosts[1] {
		http.Error(w, "forbidden host", http.StatusForbidden)
		return
	}
	h.mux.ServeHTTP(w, r)
}

func (h *handler) serveIndex(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = io.WriteString(w, strings.Replace(h.index, tokenPlaceholder, h.token, 1))
}

func (h *handler) serveHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, dashboard.Health{PID: h.pid, Version: h.o.Version, StartedAt: h.startedAt})
}

func (h *handler) serveSnapshot(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, h.snapshot())
}

// snapshot collects the current snapshot of every registered root. A
// failure to list roots gives a snapshot with no repos.
func (h *handler) snapshot() tools.DashboardSnapshot {
	t := now()
	return h.o.Collect(h.rootPaths(t), t)
}

// rootPaths returns the registered repo roots at time t. A failure to list
// them is logged and gives no roots.
func (h *handler) rootPaths(t time.Time) []string {
	roots, err := h.o.Roots(t)
	if err != nil {
		fmt.Fprintf(stderr, "sdlc dashboard: %v\n", err)
	}
	paths := make([]string, 0, len(roots))
	for _, r := range roots {
		paths = append(paths, r.Root)
	}
	return paths
}

// serveEvents streams snapshots as server-sent events: a retry line and the
// current snapshot at connect, a new snapshot only when its hash changes,
// and a ping comment every pingInterval.
func (h *handler) serveEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	if _, err := fmt.Fprintf(w, "retry: %d\n\n", sseRetryMillis); err != nil {
		return
	}

	lastHash := ""
	send := func() error {
		data, hash, err := encodeSnapshot(h.snapshot())
		if err != nil {
			fmt.Fprintf(stderr, "sdlc dashboard: %v\n", err)
			return nil
		}
		if hash == lastHash {
			return nil
		}
		if _, err := fmt.Fprintf(w, "event: snapshot\ndata: %s\n\n", data); err != nil {
			return err
		}
		lastHash = hash
		flusher.Flush()
		return nil
	}
	if send() != nil {
		return
	}

	poll := time.NewTicker(snapshotPollInterval)
	defer poll.Stop()
	ping := time.NewTicker(pingInterval)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-h.ctx.Done():
			return
		case <-poll.C:
			if send() != nil {
				return
			}
		case <-ping.C:
			if _, err := io.WriteString(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// encodeSnapshot returns the JSON of s and a hash of s without its
// GeneratedAt time, which changes at every collect.
func encodeSnapshot(s tools.DashboardSnapshot) (data []byte, hash string, err error) {
	data, err = json.Marshal(s)
	if err != nil {
		return nil, "", err
	}
	s.GeneratedAt = ""
	stable, err := json.Marshal(s)
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(stable)
	return data, hex.EncodeToString(sum[:]), nil
}

// guardMutation lets a state-changing request pass only when it comes from
// this server's own page: the Origin must be the server's loopback origin and
// X-Sdlc-Token must equal this start's token. With needsBody it also needs a
// JSON Content-Type (415) and a body of at most maxMutationBody bytes (413);
// it reads the body once and puts it back, so the route can decode it. It
// writes the JSON error and returns false when the request must stop.
func (h *handler) guardMutation(w http.ResponseWriter, r *http.Request, needsBody bool) bool {
	origin := r.Header.Get("Origin")
	if origin != h.origins[0] && origin != h.origins[1] {
		writeAPIError(w, http.StatusForbidden, "FORBIDDEN_ORIGIN", "Forbidden origin", "Open the dashboard from its own URL.")
		return false
	}
	if subtle.ConstantTimeCompare([]byte(r.Header.Get(tokenHeader)), []byte(h.token)) != 1 {
		writeAPIError(w, http.StatusForbidden, "FORBIDDEN_TOKEN", "Forbidden token", "Reload the page to get a new token.")
		return false
	}
	if !needsBody {
		return true
	}
	if mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mediaType != "application/json" {
		writeAPIError(w, http.StatusUnsupportedMediaType, "BAD_CONTENT_TYPE", "Content type must be application/json", "Send the body as application/json.")
		return false
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxMutationBody))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeAPIError(w, http.StatusRequestEntityTooLarge, "BODY_TOO_LARGE", "Request body is too large",
				fmt.Sprintf("Send a body of at most %d KiB.", maxMutationBody>>10))
		} else {
			writeAPIError(w, http.StatusBadRequest, "BAD_BODY", "Request body could not be read", "Send the body again.")
		}
		return false
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	return true
}

// serveStop accepts a stop request only when guardMutation passes it.
func (h *handler) serveStop(w http.ResponseWriter, r *http.Request) {
	if !h.guardMutation(w, r, false) {
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]bool{"stopping": true})
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	h.stop()
}

// Codes of the errors that the archive, clear, learning and delete routes
// raise themselves. The archive codes of the tools package keep their own
// constants.
const (
	// codeBadRequest is the code of a body or query that lacks a field.
	codeBadRequest = "BAD_REQUEST"
	// codeRepoNotFound is the code of a repo that the dashboard does not show.
	codeRepoNotFound = "REPO_NOT_FOUND"
	// codeClearFailed is the code of a failed cache clear.
	codeClearFailed = "CLEAR_FAILED"
	// codeLearningReadFailed is the code of a failed read of a learning body.
	codeLearningReadFailed = "LEARNING_READ_FAILED"
	// codeDeleteFailed is the code of a failed preplan, deferred or learning
	// delete that is not a bad request.
	codeDeleteFailed = "DELETE_FAILED"
	// codeNotConfigured is the code of a route whose Options function is nil.
	codeNotConfigured = "NOT_CONFIGURED"
)

// Suggestions of the errors that the archive, clear, learning and delete
// routes raise themselves.
const (
	// suggestBadRequest is the suggestion for codeBadRequest.
	suggestBadRequest = "Send the fields shown in the route example."
	// suggestReloadPage is the suggestion for codeRepoNotFound.
	suggestReloadPage = "Reload the page."
	// suggestReadLog is the suggestion for an error that the server writes to
	// server.log.
	suggestReadLog = "Read server.log, fix the named path, then try again."
	// suggestRestart is the suggestion for codeNotConfigured.
	suggestRestart = "Restart the dashboard with the current sdlc plugin."
)

// archiveRequest is the JSON body of POST /api/run-archive.
type archiveRequest struct {
	Repo           string `json:"repo"`
	RunID          string `json:"runId"`
	ConfirmStalled bool   `json:"confirmStalled"`
}

// clearRequest is the JSON body of POST /api/cache-clear.
type clearRequest struct {
	Repo string `json:"repo"`
}

// preplanDeleteRequest is the JSON body of POST /api/preplan-delete.
type preplanDeleteRequest struct {
	Repo string `json:"repo"`
	Slug string `json:"slug"`
}

// deferredDeleteRequest is the JSON body of POST /api/deferred-delete.
type deferredDeleteRequest struct {
	Repo string `json:"repo"`
	ID   string `json:"id"`
}

// learningDeleteRequest is the JSON body of POST /api/learning-delete.
type learningDeleteRequest struct {
	Repo    string `json:"repo"`
	Date    string `json:"date"`
	Heading string `json:"heading"`
}

// serveRunArchive archives one run of a registered repo. The request must
// pass guardMutation; the repo must be a display root.
func (h *handler) serveRunArchive(w http.ResponseWriter, r *http.Request) {
	if !h.guardMutation(w, r, true) {
		return
	}
	if h.o.Archive == nil {
		writeNotConfigured(w, "Run archive")
		return
	}
	var req archiveRequest
	if !decodeBody(w, r, &req) {
		return
	}
	if req.RunID == "" {
		writeBadRequest(w, "The runId field is required")
		return
	}
	t := now()
	root, ok := h.displayRoot(w, t, req.Repo)
	if !ok {
		return
	}
	out, err := h.o.Archive(tools.ArchiveRunIn{Root: root, RunID: req.RunID, ConfirmStalled: req.ConfirmStalled}, t)
	if err != nil {
		writeArchiveError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// serveCacheClear clears the cache of one registered repo. The request must
// pass guardMutation; the repo must be a display root.
func (h *handler) serveCacheClear(w http.ResponseWriter, r *http.Request) {
	if !h.guardMutation(w, r, true) {
		return
	}
	if h.o.ClearCache == nil {
		writeNotConfigured(w, "Cache clear")
		return
	}
	var req clearRequest
	if !decodeBody(w, r, &req) {
		return
	}
	t := now()
	root, ok := h.displayRoot(w, t, req.Repo)
	if !ok {
		return
	}
	out, err := h.o.ClearCache(root, t)
	if err != nil {
		fmt.Fprintf(stderr, "sdlc dashboard: cache clear: %v\n", err)
		writeAPIError(w, http.StatusInternalServerError, codeClearFailed, err.Error(), suggestReadLog)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// servePreplanDelete deletes one preplan topic file of a registered repo. The
// request must pass guardMutation; the repo must be a display root. A topic
// file that is already gone is a 200 with alreadyGone.
func (h *handler) servePreplanDelete(w http.ResponseWriter, r *http.Request) {
	if !h.guardMutation(w, r, true) {
		return
	}
	if h.o.DeletePreplan == nil {
		writeNotConfigured(w, "Preplan delete")
		return
	}
	var req preplanDeleteRequest
	if !decodeBody(w, r, &req) {
		return
	}
	if req.Slug == "" {
		writeBadRequest(w, "The slug field is required")
		return
	}
	root, ok := h.displayRoot(w, now(), req.Repo)
	if !ok {
		return
	}
	out, err := h.o.DeletePreplan(root, req.Slug)
	if err != nil {
		writeDeleteError(w, "preplan delete", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// serveDeferredDelete deletes one deferred item of a registered repo. The
// request must pass guardMutation; the repo must be a display root. An item
// that is already gone is a 200 with alreadyGone.
func (h *handler) serveDeferredDelete(w http.ResponseWriter, r *http.Request) {
	if !h.guardMutation(w, r, true) {
		return
	}
	if h.o.DeleteDeferred == nil {
		writeNotConfigured(w, "Deferred delete")
		return
	}
	var req deferredDeleteRequest
	if !decodeBody(w, r, &req) {
		return
	}
	if req.ID == "" {
		writeBadRequest(w, "The id field is required")
		return
	}
	root, ok := h.displayRoot(w, now(), req.Repo)
	if !ok {
		return
	}
	out, err := h.o.DeleteDeferred(root, req.ID)
	if err != nil {
		writeDeleteError(w, "deferred delete", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// serveLearningDelete deletes one learning entry of a registered repo. The
// request must pass guardMutation; the repo must be a display root. An entry
// that is already gone is a 200 with alreadyGone.
func (h *handler) serveLearningDelete(w http.ResponseWriter, r *http.Request) {
	if !h.guardMutation(w, r, true) {
		return
	}
	if h.o.DeleteLearning == nil {
		writeNotConfigured(w, "Learning delete")
		return
	}
	var req learningDeleteRequest
	if !decodeBody(w, r, &req) {
		return
	}
	if req.Date == "" || req.Heading == "" {
		writeBadRequest(w, "The date and heading fields are required")
		return
	}
	root, ok := h.displayRoot(w, now(), req.Repo)
	if !ok {
		return
	}
	out, err := h.o.DeleteLearning(root, req.Date, req.Heading)
	if err != nil {
		writeDeleteError(w, "learning delete", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// serveLearning returns the body of one learning entry of a registered repo.
// It only reads, so it needs no token, like the snapshot route.
func (h *handler) serveLearning(w http.ResponseWriter, r *http.Request) {
	if h.o.LearningBody == nil {
		writeNotConfigured(w, "Learning body")
		return
	}
	q := r.URL.Query()
	date, heading := q.Get("date"), q.Get("heading")
	if date == "" || heading == "" {
		writeBadRequest(w, "Date and heading are required")
		return
	}
	root, ok := h.displayRoot(w, now(), q.Get("repo"))
	if !ok {
		return
	}
	out, err := h.o.LearningBody(root, date, heading)
	if err != nil {
		fmt.Fprintf(stderr, "sdlc dashboard: learning body: %v\n", err)
		writeAPIError(w, http.StatusInternalServerError, codeLearningReadFailed, err.Error(), suggestReadLog)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// displayRoot resolves repo, the path that the page sent, to the display root
// it names. It is the one check of the repo field of the archive, clear,
// delete and learning routes: an empty repo writes the 400 error, and a repo that is not
// a display root writes the 404 error. Both return false, so a mutating route
// never acts on a path from the page that the dashboard does not show.
func (h *handler) displayRoot(w http.ResponseWriter, t time.Time, repo string) (string, bool) {
	if repo == "" {
		writeBadRequest(w, "The repo field is required")
		return "", false
	}
	root, ok := tools.ResolveDisplayRoot(h.rootPaths(t), repo)
	if !ok {
		writeAPIError(w, http.StatusNotFound, codeRepoNotFound, fmt.Sprintf("Repo %q is not a repo that the dashboard shows", repo), suggestReloadPage)
		return "", false
	}
	return root, true
}

// decodeBody decodes the JSON body that guardMutation put back into dst. A
// body that is not valid JSON of the shape of dst writes the 400 error and
// returns false.
func decodeBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	body, err := io.ReadAll(r.Body)
	if err == nil {
		err = json.Unmarshal(body, dst)
	}
	if err != nil {
		writeBadRequest(w, "Request body is not a JSON object with the fields of the route")
		return false
	}
	return true
}

// writeBadRequest writes the 400 error for a request that lacks a field or
// holds a wrong one.
func writeBadRequest(w http.ResponseWriter, message string) {
	writeAPIError(w, http.StatusBadRequest, codeBadRequest, message, suggestBadRequest)
}

// writeNotConfigured writes the 500 error for a route whose Options function
// is nil. Production always sets the function; a nil field is a wiring
// defect.
func writeNotConfigured(w http.ResponseWriter, what string) {
	writeAPIError(w, http.StatusInternalServerError, codeNotConfigured, what+" is not available on this server", suggestRestart)
}

// writeArchiveError maps an error of Options.Archive to a status and a code.
// An ArchiveError with a code of the tools package keeps that code; every
// other error, and an ArchiveError with another code, is a 500 with code
// tools.ArchiveFailed. Every 500 is also written to the server log.
func writeArchiveError(w http.ResponseWriter, err error) {
	code, status := tools.ArchiveFailed, http.StatusInternalServerError
	message, suggestion := err.Error(), suggestReadLog
	var ae *tools.ArchiveError
	if errors.As(err, &ae) {
		message = ae.Message
		if ae.Suggestion != "" {
			suggestion = ae.Suggestion
		}
		switch ae.Code {
		case tools.ArchiveBadRunID:
			code, status = ae.Code, http.StatusBadRequest
		case tools.ArchiveRunNotFound:
			code, status = ae.Code, http.StatusNotFound
		case tools.ArchiveRunActive, tools.ArchiveConfirmStalled:
			code, status = ae.Code, http.StatusConflict
		case tools.ArchiveFailed:
			// The defaults above hold: 500 with this code.
		}
	}
	if status == http.StatusInternalServerError {
		fmt.Fprintf(stderr, "sdlc dashboard: run archive: %v\n", err)
	}
	writeAPIError(w, status, code, message, suggestion)
}

// writeDeleteError maps an error of Options.DeletePreplan, DeleteDeferred or
// DeleteLearning to a status and a code. A DomainError is a 400 with code
// BAD_REQUEST. A DataError, an InfraError and any other error is a 500 with
// code DELETE_FAILED; it is also written to the server log, and a missing
// Suggestion becomes suggestReadLog. An item that is already gone is not an
// error: the delete function returns it as a result, and the route answers 200.
func writeDeleteError(w http.ResponseWriter, what string, err error) {
	var de *mcpserver.DomainError
	if errors.As(err, &de) {
		suggestion := de.Suggestion
		if suggestion == "" {
			suggestion = suggestBadRequest
		}
		writeAPIError(w, http.StatusBadRequest, codeBadRequest, de.Msg, suggestion)
		return
	}
	message, suggestion := err.Error(), ""
	var da *mcpserver.DataError
	var ie *mcpserver.InfraError
	switch {
	case errors.As(err, &da):
		message, suggestion = da.Msg, da.Suggestion
	case errors.As(err, &ie):
		message, suggestion = ie.Msg, ie.Suggestion
	}
	if suggestion == "" {
		suggestion = suggestReadLog
	}
	fmt.Fprintf(stderr, "sdlc dashboard: %s: %v\n", what, err)
	writeAPIError(w, http.StatusInternalServerError, codeDeleteFailed, message, suggestion)
}

// writeAPIError writes {"error":{"code","message","suggestion"}} with status.
func writeAPIError(w http.ResponseWriter, status int, code, message, suggestion string) {
	type apiError struct {
		Code       string `json:"code"`
		Message    string `json:"message"`
		Suggestion string `json:"suggestion"`
	}
	writeJSON(w, status, map[string]apiError{"error": {Code: code, Message: message, Suggestion: suggestion}})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(append(data, '\n'))
}
