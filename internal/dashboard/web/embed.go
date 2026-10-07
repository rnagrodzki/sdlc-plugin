package web

import "embed"

// staticFS holds the dashboard page files (index.html, app.css, app.js,
// view.js, fonts/) compiled into the binary, so the server needs no files on
// disk at run time.
//
//go:embed static
var staticFS embed.FS
