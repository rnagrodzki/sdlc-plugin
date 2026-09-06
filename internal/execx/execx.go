// Package execx is the single process-execution chokepoint for the sdlc
// plugin: every package that needs to shell out does so through Run, and
// every retry loop goes through Retry. Centralizing both here means output
// capping and retry/backoff are each implemented exactly once, so no
// downstream package can reimplement — and possibly get wrong — either
// behavior.
//
// Run and Retry port the exec/maxBuffer/retry precedent in
// scripts/lib/git.js:20-70 to Go:
//
//   - Run mirrors exec()'s soft-fail contract: any failure other than an
//     output-cap overflow returns ("", err) rather than partial output,
//     just as the source returns null on failure.
//   - Output-cap overflow is the one failure mode that is never downgraded
//     to a soft fail: it always surfaces as the distinct ErrOutputCap,
//     matching the source's overflow-always-throws behavior, so a
//     truncated result can never masquerade as a complete one — even when
//     the command itself exited successfully.
//   - Retry mirrors retryExec()'s exponential backoff, retrying exactly
//     three times with a 1s/2s/4s delay between attempts.
package execx

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"
)

// ErrOutputCap is returned by Run when a command's stdout would exceed
// Options.MaxBytes. Run never truncates output silently: overflow always
// surfaces as this distinct, non-nil error instead of a partial result that
// could be mistaken for a complete one.
var ErrOutputCap = errors.New("execx: output exceeded MaxBytes cap")

// defaultMaxBytes is the ceiling applied when Options.MaxBytes is zero,
// mirroring the source's 50MB default (scripts/lib/git.js MAX_BUFFER).
const defaultMaxBytes = 50 << 20 // 50 MiB

// Options configures Run.
type Options struct {
	// Dir sets the command's working directory. Empty means the current
	// process's working directory.
	Dir string
	// MaxBytes caps the number of stdout bytes Run collects before
	// failing with ErrOutputCap. Zero means defaultMaxBytes (50MB).
	MaxBytes int64
	// Stdin, when non-nil, is piped to the command's standard input.
	Stdin io.Reader
}

// Run executes cmd with args and returns its trimmed stdout.
//
// Any failure other than an output-cap overflow is a soft fail: Run returns
// an empty string alongside a non-nil error, discarding whatever partial
// stdout the command produced before failing — mirroring the source's
// null-return-on-failure behavior so callers never mistake partial output
// for a complete result.
//
// If the command writes more than opt.MaxBytes (default 50MB) to stdout,
// Run returns an error wrapping ErrOutputCap. This overflow case is never
// downgraded to a soft fail and takes precedence over any other outcome —
// it is returned even if the command's own exit code was success — matching
// the source's overflow-always-throws behavior.
func Run(cmd string, args []string, opt Options) (string, error) {
	maxBytes := opt.MaxBytes
	if maxBytes <= 0 {
		maxBytes = defaultMaxBytes
	}

	c := exec.Command(cmd, args...)
	c.Dir = opt.Dir
	c.Stdin = opt.Stdin

	out := &cappedBuffer{limit: maxBytes}
	c.Stdout = out
	var stderr bytes.Buffer
	c.Stderr = &stderr

	runErr := c.Run()

	// Overflow takes precedence over any other outcome: even a command
	// that exits 0 after overflowing the cap must not have its truncated
	// output mistaken for a complete result.
	if out.exceeded {
		return "", fmt.Errorf("execx: %s: %w", cmd, ErrOutputCap)
	}
	if runErr != nil {
		return "", fmt.Errorf("execx: %s %s: %w", cmd, strings.Join(args, " "), runErr)
	}

	return strings.TrimSpace(out.buf.String()), nil
}

// cappedBuffer accumulates writes up to limit bytes. Once the limit would be
// exceeded it stops copying into buf and just records that it was exceeded;
// it always reports a successful write to its caller (io.Copy, via
// exec.Cmd) so the command is left to run to completion instead of racing a
// stalled pipe against a reader that stopped early.
type cappedBuffer struct {
	buf      bytes.Buffer
	limit    int64
	exceeded bool
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	if c.exceeded {
		return len(p), nil
	}
	if int64(c.buf.Len())+int64(len(p)) > c.limit {
		c.exceeded = true
		return len(p), nil
	}
	c.buf.Write(p)
	return len(p), nil
}

// retryAttempts is the number of retries Retry performs after the initial
// attempt (four attempts total), matching the "retries exactly 3 times"
// acceptance criteria.
const retryAttempts = 3

// retryBaseDelay is the delay before the first retry; it doubles before
// each subsequent retry, producing the 1s/2s/4s backoff sequence across the
// three retries above.
const retryBaseDelay = 1 * time.Second

// sleep performs Retry's backoff delay. It is a package variable — rather
// than a hardcoded time.Sleep call — so tests can inject a no-op clock and
// run the full retry sequence instantly instead of waiting on real
// wall-clock time.
var sleep = time.Sleep

// Retry calls fn and, if it returns a non-nil error, retries up to
// retryAttempts more times (four attempts total), waiting retryBaseDelay
// before the first retry and doubling the wait before each subsequent one
// (1s, 2s, 4s). It returns nil as soon as fn succeeds, or fn's last error if
// every attempt fails.
func Retry(fn func() error) error {
	err := fn()
	delay := retryBaseDelay
	for attempt := 1; attempt <= retryAttempts && err != nil; attempt++ {
		sleep(delay)
		err = fn()
		delay *= 2
	}
	return err
}
