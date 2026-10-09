// Command design-preview serves the dashboard design draft in design/dashboard
// next to the real dashboard API. It runs the start rule first, prints one
// line for each result, and serves until POST /__design/stop.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/dashboard"
	"github.com/rnagrodzki/sdlc-plugin/internal/execx"
)

// listenAddr is the fixed address of the preview. A fixed address lets a
// second start report that a preview may already run.
const listenAddr = "127.0.0.1:55570"

// Exit codes of the program.
const (
	// exitStopped is the code after the stop route ended the preview.
	exitStopped = 0
	// exitError is the code of every error.
	exitError = 2
	// exitDraftHeld is the code of case d: nothing changed and nothing is served.
	exitDraftHeld = 3
)

// stopGrace is how long Stop waits for active connections before it closes them.
const stopGrace = 2 * time.Second

// readHeaderTimeout is how long the server waits for the headers of a request.
const readHeaderTimeout = 10 * time.Second

// usageText names the one flag in the flag error lines.
const usageText = "Usage: -mode auto|fresh|continue."

// runConfig holds what main resolves. Tests pass a t.TempDir() git repo and "127.0.0.1:0".
type runConfig struct {
	RepoRoot   string                                 // git top level
	Addr       string                                 // listen address
	ReadRecord func() (dashboard.ServerRecord, error) // finds the real dashboard
}

// run is main without os.Exit. Order: parse -mode, listen on c.Addr, StartRule,
// serve until Stop. Exit codes: 0 stopped, 2 error, 3 case d. Marker lines and
// the serving line go to stdout. Error lines go to stderr.
func run(args []string, c runConfig, stdout, stderr io.Writer) int {
	mode, err := parseMode(args)
	if err != nil {
		return fail(stderr, err.Error())
	}

	// Listen before the start rule: a start that finds the address bound must
	// not change the draft of a preview that may still run.
	ln, err := net.Listen("tcp", c.Addr)
	if err != nil {
		return fail(stderr, listenErrorText(c.Addr, err))
	}
	defer ln.Close()

	outcome, err := StartRule(c.RepoRoot, mode)
	if err != nil {
		return fail(stderr, err.Error())
	}
	for _, line := range outcome.Lines {
		fmt.Fprintln(stdout, line)
	}
	if !outcome.Serve {
		return exitDraftHeld
	}

	return serve(ln, c, stdout, stderr)
}

// parseMode reads the -mode flag from args. Any other flag, any other
// argument and any value other than auto, fresh or continue is an error.
func parseMode(args []string) (Mode, error) {
	flags := flag.NewFlagSet("design-preview", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	value := flags.String("mode", string(ModeAuto), "auto, fresh or continue")
	if err := flags.Parse(args); err != nil {
		return "", fmt.Errorf("%v. %s", err, usageText)
	}
	if flags.NArg() > 0 {
		return "", fmt.Errorf("unexpected argument %q. %s", flags.Arg(0), usageText)
	}

	mode := Mode(*value)
	if err := mode.validate(); err != nil {
		return "", err
	}

	return mode, nil
}

// listenErrorText gives the text of the error line for a failed listen on
// addr. A bound address names the URL, because a preview may run there.
func listenErrorText(addr string, err error) string {
	if errors.Is(err, syscall.EADDRINUSE) {
		return fmt.Sprintf("%s is in use. A preview may still run: http://%s/", addr, addr)
	}

	// A *net.OpError repeats the address, so only its cause is kept.
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		err = opErr.Err
	}

	return fmt.Sprintf("%s: %v", addr, err)
}

// fail prints text as an error line to stderr and returns exitError.
func fail(stderr io.Writer, text string) int {
	fmt.Fprintf(stderr, "design preview: error — %s\n", text)

	return exitError
}

// serve prints the serving line, then serves ln until Stop ends the server.
// Stop waits stopGrace for active connections, then closes them. A proxied
// /api/events stream does not end by itself, so Shutdown alone would wait for it.
func serve(ln net.Listener, c runConfig, stdout, stderr io.Writer) int {
	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		return fail(stderr, fmt.Sprintf("%s: listener address %v is not a TCP address", c.Addr, ln.Addr()))
	}
	port := addr.Port

	srv := &http.Server{ReadHeaderTimeout: readHeaderTimeout}
	stopped := make(chan struct{})
	var once sync.Once
	stop := func() {
		once.Do(func() {
			defer close(stopped)
			ctx, cancel := context.WithTimeout(context.Background(), stopGrace)
			defer cancel()
			// The grace time ending is expected with an open stream; Close ends the rest.
			_ = srv.Shutdown(ctx)
			_ = srv.Close()
		})
	}
	srv.Handler = newHandler(serverOptions{
		DraftDir:   filepath.Join(c.RepoRoot, draftDir),
		Port:       port,
		ReadRecord: c.ReadRecord,
		Stop:       stop,
	}, registerAPI)

	fmt.Fprintf(stdout, "design preview: serving http://127.0.0.1:%d/ (pid %d)\n", port, os.Getpid())

	if err := srv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
		return fail(stderr, fmt.Sprintf("%s: %v", c.Addr, err))
	}
	// Serve returns as soon as Shutdown starts. Wait for Stop to finish.
	<-stopped

	return exitStopped
}

// main resolves the repo root, then runs the preview.
func main() {
	root, err := execx.Run("git", []string{"rev-parse", "--show-toplevel"}, execx.Options{})
	if err != nil {
		os.Exit(fail(os.Stderr, err.Error()))
	}

	os.Exit(run(os.Args[1:], runConfig{RepoRoot: root, Addr: listenAddr, ReadRecord: dashboard.ReadServerRecord}, os.Stdout, os.Stderr))
}
