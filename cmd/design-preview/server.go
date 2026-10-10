package main

import (
	"bytes"
	_ "embed" // marks.js is embedded into the program
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"

	"github.com/rnagrodzki/sdlc-plugin/internal/dashboard"
)

// maxIndexBytes is the largest draft index.html the page route accepts.
const maxIndexBytes = 4 << 20

// depsTagOpen opens the tag that carries the dependency data to marks.js.
const depsTagOpen = `<script id="design-deps" type="application/json">`

// marksTag loads the marks script. It follows the design-deps tag.
const marksTag = `<script src="/__design/marks.js"></script>`

// marksJS is the marks script, served at /__design/marks.js.
//
//go:embed marks.js
var marksJS []byte

// serverOptions is what the page routes and the extra routes need.
type serverOptions struct {
	DraftDir   string                                 // <repo>/design/dashboard
	Port       int                                    // listener port, for the Host check
	ReadRecord func() (dashboard.ServerRecord, error) // dashboard.ReadServerRecord in main
	Stop       func()                                 // shuts the http.Server down
}

// depsOK is the content of the design-deps tag when the dependency file loads.
type depsOK struct {
	OK           bool         `json:"ok"`
	Dependencies []Dependency `json:"dependencies"`
}

// depsFailed is the content of the design-deps tag when the dependency file
// does not load. Error names the file.
type depsFailed struct {
	OK    bool   `json:"ok"`
	Error string `json:"error"`
}

// newHandler registers the page routes, calls each extra function on the same
// mux, and wraps every route in the Host check.
func newHandler(o serverOptions, extra ...func(*http.ServeMux, serverOptions)) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", serveDraftIndex(o))
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.Dir(filepath.Join(o.DraftDir, "static")))))
	mux.HandleFunc("GET /__design/marks.js", serveMarks)
	for _, register := range extra {
		register(mux, o)
	}

	hosts := [2]string{fmt.Sprintf("127.0.0.1:%d", o.Port), fmt.Sprintf("localhost:%d", o.Port)}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A page on another site can reach 127.0.0.1 through DNS rebinding; its
		// requests carry that site's host name, so only loopback names pass.
		if r.Host != hosts[0] && r.Host != hosts[1] {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		// The draft changes while the page is open. Without this header the file
		// server sends Last-Modified only, and a browser may reuse an old script or
		// style file from its cache after the user reloads.
		w.Header().Set("Cache-Control", "no-store")
		mux.ServeHTTP(w, r)
	})
}

// serveMarks answers with the embedded marks script.
func serveMarks(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/javascript")
	_, _ = w.Write(marksJS)
}

// serveDraftIndex answers with the draft page and the dependency tags.
// A page that cannot be read or has no </head> tag gives status 500 and a body
// that names the file.
func serveDraftIndex(o serverOptions) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		page, err := buildIndexPage(o.DraftDir)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(page)
	}
}

// buildIndexPage reads static/index.html of the draft folder dir and inserts the
// design-deps tag and the marks script tag before </head>. A dependency file
// that does not load is not an error here: the tag then carries the message.
func buildIndexPage(dir string) ([]byte, error) {
	indexPath := filepath.Join(dir, "static", "index.html")
	page, err := readDraftIndex(indexPath)
	if err != nil {
		return nil, err
	}
	at := bytes.Index(page, []byte("</head>"))
	if at < 0 {
		return nil, fmt.Errorf("%s: no </head> tag", indexPath)
	}

	depsJSON, err := dependencyTagJSON(filepath.Join(dir, "dependencies.json"))
	if err != nil {
		return nil, err
	}

	out := make([]byte, 0, len(page)+len(depsTagOpen)+len(depsJSON)+len("</script>")+len(marksTag))
	out = append(out, page[:at]...)
	out = append(out, depsTagOpen...)
	out = append(out, depsJSON...)
	out = append(out, "</script>"...)
	out = append(out, marksTag...)
	out = append(out, page[at:]...)

	return out, nil
}

// readDraftIndex reads path with a size limit. Every error names path.
func readDraftIndex(path string) ([]byte, error) {
	data, err := readBounded(path, maxIndexBytes)
	if err != nil {
		return nil, pathError(path, err)
	}

	return data, nil
}

// dependencyTagJSON loads path and returns the JSON text for the design-deps
// tag. encoding/json writes each "<" as a JSON unicode escape, so a need
// or a sample that holds "</script>" or "<!--" cannot end the tag.
func dependencyTagJSON(path string) ([]byte, error) {
	var payload any
	deps, err := LoadDependencies(path)
	if err != nil {
		payload = depsFailed{OK: false, Error: err.Error()}
	} else {
		// LoadDependencies never returns a nil slice without an error: the
		// root must be an array, and encoding/json decodes [] to an empty slice.
		payload = depsOK{OK: true, Dependencies: deps}
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	return data, nil
}
