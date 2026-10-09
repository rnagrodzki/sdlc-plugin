package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rnagrodzki/sdlc-plugin/internal/dashboard"
)

const (
	// serverPort is the port the test handlers expect in the Host header.
	serverPort = 4242
	// serverHost is a Host header value that passes the Host check.
	serverHost = "127.0.0.1:4242"
	// serverIndex is a draft page with one </head> tag.
	serverIndex = "<!doctype html>\n<html>\n<head>\n<title>t</title>\n</head>\n<body>{{SDLC_TOKEN}}</body>\n</html>\n"
	// serverOneDep is a dependency file with one record that has a sample.
	serverOneDep = `[{"id":"D1","kind":"data","need":"Show the run name.","elements":["#run"],"sample":{"name":"run-1"}}]`
)

// serverPut writes content to rel inside dir and creates the folders on the way.
func serverPut(t *testing.T, dir, rel string, content []byte) string {
	t.Helper()
	path := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("make folder: %v", err)
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	return path
}

// serverDraft returns a new draft folder with a static/index.html page and,
// when deps is not empty, a dependencies.json file.
func serverDraft(t *testing.T, index, deps string) string {
	t.Helper()
	dir := t.TempDir()
	serverPut(t, dir, filepath.Join("static", "index.html"), []byte(index))
	if deps != "" {
		serverPut(t, dir, "dependencies.json", []byte(deps))
	}

	return dir
}

// serverHandler builds the handler for the draft folder dir.
func serverHandler(dir string, extra ...func(*http.ServeMux, serverOptions)) http.Handler {
	return newHandler(serverOptions{
		DraftDir: dir,
		Port:     serverPort,
		ReadRecord: func() (dashboard.ServerRecord, error) {
			return dashboard.ServerRecord{}, errors.New("not used by the page routes")
		},
		Stop: func() {},
	}, extra...)
}

// serverDo sends one request with the given Host to h and returns the answer.
func serverDo(h http.Handler, method, path, host string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	req.Host = host
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	return rec
}

// serverGet sends a GET request with a Host that passes the Host check.
func serverGet(h http.Handler, path string) *httptest.ResponseRecorder {
	return serverDo(h, http.MethodGet, path, serverHost)
}

// serverAssertReply checks the status, the Content-Type header and the body of rec.
func serverAssertReply(t *testing.T, rec *httptest.ResponseRecorder, status int, contentType, body string) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status = %d, want %d; body %q", rec.Code, status, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != contentType {
		t.Errorf("Content-Type = %q, want %q", got, contentType)
	}
	if got := rec.Body.String(); got != body {
		t.Errorf("body = %q, want %q", got, body)
	}
}

// serverTagText returns the text inside the design-deps tag of page. The tag
// ends at the first closing script tag after it.
func serverTagText(t *testing.T, page string) string {
	t.Helper()
	start := strings.Index(page, depsTagOpen)
	if start < 0 {
		t.Fatalf("no design-deps tag in %q", page)
	}
	rest := page[start+len(depsTagOpen):]
	end := strings.Index(rest, "</script>")
	if end < 0 {
		t.Fatalf("design-deps tag has no end in %q", page)
	}

	return rest[:end]
}

// serverTagPayload decodes the design-deps tag of page.
func serverTagPayload(t *testing.T, page string) (ok bool, errText string, deps []Dependency) {
	t.Helper()
	var payload struct {
		OK           bool         `json:"ok"`
		Error        string       `json:"error"`
		Dependencies []Dependency `json:"dependencies"`
	}
	if err := json.Unmarshal([]byte(serverTagText(t, page)), &payload); err != nil {
		t.Fatalf("design-deps tag is not JSON: %v", err)
	}

	return payload.OK, payload.Error, payload.Dependencies
}

// serverWrap returns the page that the handler must give for a draft page
// serverIndex and the tag JSON depsJSON.
func serverWrap(depsJSON string) string {
	return strings.Replace(serverIndex, "</head>", depsTagOpen+depsJSON+"</script>"+marksTag+"</head>", 1)
}

// serverSkipIfRoot skips the test when the user is root. Root reads every file.
func serverSkipIfRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root ignores file modes")
	}
}

func TestServerPage(t *testing.T) {
	t.Run("GET / inserts the design-deps tag and the marks tag before </head>", func(t *testing.T) {
		dir := serverDraft(t, serverIndex, serverOneDep)
		rec := serverGet(serverHandler(dir), "/")

		want := serverWrap(`{"ok":true,"dependencies":[{"id":"D1","kind":"data","need":"Show the run name.","elements":["#run"],"sample":{"name":"run-1"}}]}`)
		serverAssertReply(t, rec, http.StatusOK, "text/html; charset=utf-8", want)
		if got := rec.Header().Get("Cache-Control"); got != "no-store" {
			t.Errorf("Cache-Control = %q, want no-store", got)
		}
		if !strings.Contains(rec.Body.String(), "{{SDLC_TOKEN}}") {
			t.Error("the placeholder of the draft page must stay in the body")
		}
	})

	t.Run("GET / with an empty record list writes [] and elements []", func(t *testing.T) {
		empty := serverGet(serverHandler(serverDraft(t, serverIndex, "[]")), "/")
		serverAssertReply(t, empty, http.StatusOK, "text/html; charset=utf-8", serverWrap(`{"ok":true,"dependencies":[]}`))

		noElements := serverDraft(t, serverIndex, `[{"id":"D3","kind":"flow","need":"Open the run."}]`)
		rec := serverGet(serverHandler(noElements), "/")
		serverAssertReply(t, rec, http.StatusOK, "text/html; charset=utf-8",
			serverWrap(`{"ok":true,"dependencies":[{"id":"D3","kind":"flow","need":"Open the run.","elements":[]}]}`))
	})

	t.Run("GET / with a bad dependency file still gives 200 and the error", func(t *testing.T) {
		cases := []struct {
			name string
			deps string // empty means no dependencies.json file
			want string // text that the error must hold after the path
		}{
			{"missing file", "", "not found"},
			{"not JSON", "{oops", "not valid JSON"},
			{"root is not an array", `{"id":"D1"}`, "root must be a JSON array"},
			{"bad record", `[{"id":"X1","kind":"data","need":"n"}]`, "record 1: id must match D<number>"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				dir := serverDraft(t, serverIndex, tc.deps)
				rec := serverGet(serverHandler(dir), "/")

				if rec.Code != http.StatusOK {
					t.Fatalf("status = %d, want 200; body %q", rec.Code, rec.Body.String())
				}
				if got := rec.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
					t.Errorf("Content-Type = %q", got)
				}
				ok, errText, deps := serverTagPayload(t, rec.Body.String())
				if ok || len(deps) != 0 {
					t.Errorf("ok = %v, dependencies = %v, want ok false and no records", ok, deps)
				}
				path := filepath.Join(dir, "dependencies.json")
				if !strings.HasPrefix(errText, path+": ") || !strings.Contains(errText, tc.want) {
					t.Errorf("error = %q, want prefix %q and text %q", errText, path+": ", tc.want)
				}
				if !strings.Contains(rec.Body.String(), marksTag+"</head>") {
					t.Error("the marks tag must follow the design-deps tag before </head>")
				}
			})
		}
	})

	t.Run("GET / writes each < of the dependency data as a JSON unicode escape", func(t *testing.T) {
		deps := `[{"id":"D1","kind":"data","need":"Close </script> and <!-- open","elements":["a[title='</script>']"],"sample":{"html":"</script><!--"}}]`
		dir := serverDraft(t, serverIndex, deps)
		rec := serverGet(serverHandler(dir), "/")
		page := rec.Body.String()

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body %q", rec.Code, page)
		}
		tag := serverTagText(t, page)
		if strings.Contains(tag, "<") {
			t.Errorf("tag text holds a raw <: %q", tag)
		}
		// The JSON escape for "<" is a backslash, then "u003c". It is built from
		// parts so that a tool which unescapes JSON in source text cannot turn it
		// into a raw "<" and weaken this check.
		escLT := `\` + "u003c"
		escGT := `\` + "u003e"
		for _, want := range []string{escLT + "/script" + escGT, escLT + "!--"} {
			if !strings.Contains(tag, want) {
				t.Errorf("tag text %q does not hold %q", tag, want)
			}
		}
		// The page has no script of its own, so only the two inserted tags close.
		if got := strings.Count(page, "</script>"); got != 2 {
			t.Errorf("page holds %d </script> tags, want 2", got)
		}
		if strings.Contains(page, "<!--") {
			t.Error("page holds a raw <!-- from the dependency data")
		}

		ok, _, got := serverTagPayload(t, page)
		if !ok || len(got) != 1 {
			t.Fatalf("ok = %v, records = %d, want ok true and 1 record", ok, len(got))
		}
		if got[0].Need != "Close </script> and <!-- open" {
			t.Errorf("need = %q after the JSON decode", got[0].Need)
		}
		var sample struct {
			HTML string `json:"html"`
		}
		if err := json.Unmarshal(got[0].Sample, &sample); err != nil || sample.HTML != "</script><!--" {
			t.Errorf("sample = %s (html %q, err %v) after the JSON decode", got[0].Sample, sample.HTML, err)
		}
	})

	t.Run("GET / answers 500 and names the page path when the page cannot be served", func(t *testing.T) {
		cases := []struct {
			name  string
			setup func(t *testing.T, dir, path string)
			// want is the exact body when exact is true, else text that the body must hold.
			want  string
			exact bool
		}{
			{
				name: "over 4 MiB",
				setup: func(t *testing.T, dir, path string) {
					page := append([]byte("<html><head></head>"), bytes.Repeat([]byte("a"), 4<<20)...)
					serverPut(t, dir, filepath.Join("static", "index.html"), page)
				},
				want:  "%s: larger than 4 MiB\n",
				exact: true,
			},
			{
				name: "no </head> tag",
				setup: func(t *testing.T, dir, path string) {
					serverPut(t, dir, filepath.Join("static", "index.html"), []byte("<html><body>x</body></html>"))
				},
				want:  "%s: no </head> tag\n",
				exact: true,
			},
			{
				name: "file missing",
				setup: func(t *testing.T, dir, path string) {
					if err := os.Remove(path); err != nil {
						t.Fatalf("remove page: %v", err)
					}
				},
				want:  "%s: not found\n",
				exact: true,
			},
			{
				name: "a folder at the page path",
				setup: func(t *testing.T, dir, path string) {
					if err := os.Remove(path); err != nil {
						t.Fatalf("remove page: %v", err)
					}
					if err := os.Mkdir(path, 0o755); err != nil {
						t.Fatalf("make folder: %v", err)
					}
				},
				want: "is a directory",
			},
			{
				name: "no permission",
				setup: func(t *testing.T, dir, path string) {
					serverSkipIfRoot(t)
					if err := os.Chmod(path, 0); err != nil {
						t.Fatalf("chmod: %v", err)
					}
				},
				want: "permission denied",
			},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				dir := serverDraft(t, serverIndex, "[]")
				path := filepath.Join(dir, "static", "index.html")
				tc.setup(t, dir, path)

				rec := serverGet(serverHandler(dir), "/")

				if rec.Code != http.StatusInternalServerError {
					t.Fatalf("status = %d, want 500; body %q", rec.Code, rec.Body.String())
				}
				if got := rec.Header().Get("Content-Type"); got != "text/plain; charset=utf-8" {
					t.Errorf("Content-Type = %q, want text/plain; charset=utf-8", got)
				}
				body := rec.Body.String()
				if tc.exact {
					if want := fmt.Sprintf(tc.want, path); body != want {
						t.Errorf("body = %q, want %q", body, want)
					}
					return
				}
				if !strings.HasPrefix(body, path+": ") || !strings.Contains(body, tc.want) {
					t.Errorf("body = %q, want prefix %q and text %q", body, path+": ", tc.want)
				}
			})
		}
	})

	t.Run("GET / accepts a page of exactly 4 MiB", func(t *testing.T) {
		head := []byte("<html><head></head>")
		page := append(head, bytes.Repeat([]byte("a"), 4<<20-len(head))...)
		dir := serverDraft(t, serverIndex, "[]")
		serverPut(t, dir, filepath.Join("static", "index.html"), page)

		rec := serverGet(serverHandler(dir), "/")

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body %q", rec.Code, rec.Body.String())
		}
		inserted := depsTagOpen + `{"ok":true,"dependencies":[]}</script>` + marksTag
		if !strings.HasPrefix(rec.Body.String(), "<html><head>"+inserted+"</head>") {
			t.Error("body does not start with the inserted tags")
		}
		if want := len(page) + len(inserted); rec.Body.Len() != want {
			t.Errorf("body length = %d, want %d", rec.Body.Len(), want)
		}
	})

	t.Run("GET /static/ serves a draft file and answers a missing file with a plain 404", func(t *testing.T) {
		dir := serverDraft(t, serverIndex, "[]")
		serverPut(t, dir, filepath.Join("static", "css", "app.css"), []byte("body{color:red}"))
		h := serverHandler(dir)

		rec := serverGet(h, "/static/css/app.css")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/css") {
			t.Errorf("Content-Type = %q, want text/css", got)
		}
		if got := rec.Body.String(); got != "body{color:red}" {
			t.Errorf("body = %q", got)
		}

		missing := serverGet(h, "/static/css/none.css")
		serverAssertReply(t, missing, http.StatusNotFound, "text/plain; charset=utf-8", "404 page not found\n")
	})

	t.Run("GET /__design/marks.js serves the embedded script", func(t *testing.T) {
		rec := serverGet(serverHandler(serverDraft(t, serverIndex, "[]")), "/__design/marks.js")

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		if got := rec.Header().Get("Content-Type"); got != "application/javascript" {
			t.Errorf("Content-Type = %q, want application/javascript", got)
		}
		if len(marksJS) == 0 || !bytes.Contains(marksJS, []byte("designDep")) {
			t.Fatalf("embedded marksJS is empty or is not the marks script (%d bytes)", len(marksJS))
		}
		if !bytes.Equal(rec.Body.Bytes(), marksJS) {
			t.Errorf("body is not the embedded script (%d bytes, want %d)", rec.Body.Len(), len(marksJS))
		}
	})

	t.Run("a Host other than the loopback names with the port gives 403 on every route", func(t *testing.T) {
		dir := serverDraft(t, serverIndex, serverOneDep)
		extra := func(mux *http.ServeMux, _ serverOptions) {
			mux.HandleFunc("GET /__design/extra", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("extra")) })
		}
		h := serverHandler(dir, extra)
		paths := []string{"/", "/static/index.html", "/__design/marks.js", "/__design/extra", "/missing"}
		hosts := []string{"evil.example:4242", "127.0.0.1:9999", "127.0.0.1", "localhost", "", "127.0.0.1:4242.evil.example", "[::1]:4242"}

		for _, host := range hosts {
			for _, path := range paths {
				rec := serverDo(h, http.MethodGet, path, host)
				serverAssertReply(t, rec, http.StatusForbidden, "text/plain; charset=utf-8", "forbidden host\n")
				if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
					t.Errorf("host %q path %s: X-Content-Type-Options header is missing", host, path)
				}
			}
		}
		for _, host := range []string{"127.0.0.1:4242", "localhost:4242"} {
			if rec := serverDo(h, http.MethodGet, "/__design/extra", host); rec.Code != http.StatusOK || rec.Body.String() != "extra" {
				t.Errorf("host %q: status = %d, body = %q, want 200 and extra", host, rec.Code, rec.Body.String())
			}
			if rec := serverDo(h, http.MethodGet, "/", host); rec.Code != http.StatusOK {
				t.Errorf("host %q: GET / status = %d, want 200", host, rec.Code)
			}
		}
	})

	t.Run("extra functions register on the same mux and receive the options", func(t *testing.T) {
		dir := serverDraft(t, serverIndex, "[]")
		first := func(mux *http.ServeMux, o serverOptions) {
			mux.HandleFunc("GET /__design/first", func(w http.ResponseWriter, _ *http.Request) {
				_, _ = fmt.Fprintf(w, "port=%d draft=%s", o.Port, o.DraftDir)
			})
		}
		second := func(mux *http.ServeMux, _ serverOptions) {
			mux.HandleFunc("GET /__design/second", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("second")) })
		}
		h := serverHandler(dir, first, second)

		serverAssertReply(t, serverGet(h, "/__design/first"), http.StatusOK, "text/plain; charset=utf-8",
			fmt.Sprintf("port=%d draft=%s", serverPort, dir))
		serverAssertReply(t, serverGet(h, "/__design/second"), http.StatusOK, "text/plain; charset=utf-8", "second")
		if rec := serverGet(h, "/"); rec.Code != http.StatusOK {
			t.Errorf("GET / status = %d, want 200", rec.Code)
		}
	})

	t.Run("a method other than GET gives 405", func(t *testing.T) {
		h := serverHandler(serverDraft(t, serverIndex, "[]"))

		for _, path := range []string{"/", "/__design/marks.js", "/static/index.html"} {
			rec := serverDo(h, http.MethodPost, path, serverHost)
			serverAssertReply(t, rec, http.StatusMethodNotAllowed, "text/plain; charset=utf-8", "Method Not Allowed\n")
			if allow := rec.Header().Get("Allow"); !strings.Contains(allow, "GET") {
				t.Errorf("POST %s: Allow = %q, want GET in it", path, allow)
			}
		}
	})
}
