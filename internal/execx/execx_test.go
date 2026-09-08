package execx

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRun_Success(t *testing.T) {
	got, err := Run("sh", []string{"-c", "printf hello"}, Options{})
	if err != nil {
		t.Fatalf("Run: unexpected error: %v", err)
	}
	if got != "hello" {
		t.Fatalf("Run: got %q, want %q", got, "hello")
	}
}

func TestRun_TrimsTrailingNewline(t *testing.T) {
	got, err := Run("echo", []string{"hello"}, Options{})
	if err != nil {
		t.Fatalf("Run: unexpected error: %v", err)
	}
	if got != "hello" {
		t.Fatalf("Run: got %q, want %q", got, "hello")
	}
}

func TestRun_Stdin(t *testing.T) {
	got, err := Run("cat", nil, Options{Stdin: strings.NewReader("piped-input")})
	if err != nil {
		t.Fatalf("Run: unexpected error: %v", err)
	}
	if got != "piped-input" {
		t.Fatalf("Run: got %q, want %q", got, "piped-input")
	}
}

func TestRun_Dir(t *testing.T) {
	dir := t.TempDir()

	got, err := Run("pwd", nil, Options{Dir: dir})
	if err != nil {
		t.Fatalf("Run: unexpected error: %v", err)
	}

	wantResolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("EvalSymlinks(%s): %v", dir, err)
	}
	gotResolved, err := filepath.EvalSymlinks(got)
	if err != nil {
		t.Fatalf("EvalSymlinks(%s): %v", got, err)
	}
	if gotResolved != wantResolved {
		t.Fatalf("Run(pwd) in %s = %q, want %q", dir, got, dir)
	}
}

// TestRun_CommandFailureIsSoftFail checks the "soft-fail (empty string +
// error) mirrors source null-return" decision: a command that writes
// output and then exits non-zero must not have that partial output
// returned — the caller sees ("", err), matching the source returning null
// on failure rather than a truncated success.
func TestRun_CommandFailureIsSoftFail(t *testing.T) {
	got, err := Run("sh", []string{"-c", "printf partial; exit 1"}, Options{})
	if err == nil {
		t.Fatalf("Run: expected error for non-zero exit, got nil")
	}
	if got != "" {
		t.Fatalf("Run: got %q on failure, want empty string (partial output must be discarded)", got)
	}
}

// TestRun_OutputCapOverflow checks that output beyond MaxBytes returns the
// distinct ErrOutputCap rather than a silently truncated result — and that
// this holds even though the underlying command exits successfully,
// matching the "overflow stays hard" decision (it is never downgraded to a
// soft fail just because the command itself succeeded).
func TestRun_OutputCapOverflow(t *testing.T) {
	got, err := Run("sh", []string{"-c", "printf '0123456789'"}, Options{MaxBytes: 5})
	if err == nil {
		t.Fatalf("Run: expected ErrOutputCap, got nil error and output %q", got)
	}
	if !errors.Is(err, ErrOutputCap) {
		t.Fatalf("Run: expected errors.Is(err, ErrOutputCap), got %v", err)
	}
	if got != "" {
		t.Fatalf("Run: got %q on overflow, want empty string (output must never be silently truncated)", got)
	}
}

// TestRun_OutputCapOverflowTakesPrecedenceOverExecError checks that when a
// command both overflows the cap AND exits non-zero, the caller sees the
// distinct ErrOutputCap rather than a generic exec-failure error — overflow
// is always surfaced, never masked by an unrelated failure.
func TestRun_OutputCapOverflowTakesPrecedenceOverExecError(t *testing.T) {
	got, err := Run("sh", []string{"-c", "printf '0123456789'; exit 1"}, Options{MaxBytes: 5})
	if !errors.Is(err, ErrOutputCap) {
		t.Fatalf("Run: expected errors.Is(err, ErrOutputCap), got %v", err)
	}
	if got != "" {
		t.Fatalf("Run: got %q on overflow, want empty string", got)
	}
}

func TestRun_DefaultMaxBytesAppliesWhenZero(t *testing.T) {
	// A small command with Options{} (MaxBytes: 0) must not be treated as
	// a zero-byte cap — zero means "use defaultMaxBytes (50MB)".
	got, err := Run("sh", []string{"-c", "printf hello"}, Options{})
	if err != nil {
		t.Fatalf("Run: unexpected error: %v", err)
	}
	if got != "hello" {
		t.Fatalf("Run: got %q, want %q", got, "hello")
	}
}

// withStubClock replaces the package-level sleep var with a no-op that
// records every requested delay, restoring the real time.Sleep on cleanup.
// This lets Retry tests exercise the full backoff sequence without waiting
// on real wall-clock time.
func withStubClock(t *testing.T) *[]time.Duration {
	t.Helper()
	original := sleep
	var delays []time.Duration
	sleep = func(d time.Duration) {
		delays = append(delays, d)
	}
	t.Cleanup(func() { sleep = original })
	return &delays
}

func TestRetry_SucceedsFirstTry(t *testing.T) {
	delays := withStubClock(t)

	calls := 0
	err := Retry(func() error {
		calls++
		return nil
	})
	if err != nil {
		t.Fatalf("Retry: unexpected error: %v", err)
	}
	if calls != 1 {
		t.Fatalf("Retry: fn called %d times, want 1", calls)
	}
	if len(*delays) != 0 {
		t.Fatalf("Retry: sleep called with %v, want no calls", *delays)
	}
}

func TestRetry_SucceedsAfterRetries(t *testing.T) {
	delays := withStubClock(t)

	calls := 0
	err := Retry(func() error {
		calls++
		if calls < 3 {
			return errors.New("transient failure")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Retry: unexpected error: %v", err)
	}
	if calls != 3 {
		t.Fatalf("Retry: fn called %d times, want 3", calls)
	}
	want := []time.Duration{1 * time.Second, 2 * time.Second}
	if !durationsEqual(*delays, want) {
		t.Fatalf("Retry: sleep called with %v, want %v", *delays, want)
	}
}

// TestRetry_ExhaustsAllAttempts checks the acceptance criteria directly:
// Retry retries exactly 3 times (four attempts total) with 1s/2s/4s
// backoff, and returns the last error once every attempt has failed.
func TestRetry_ExhaustsAllAttempts(t *testing.T) {
	delays := withStubClock(t)

	calls := 0
	lastErr := errors.New("permanent failure")
	err := Retry(func() error {
		calls++
		return lastErr
	})
	if !errors.Is(err, lastErr) {
		t.Fatalf("Retry: got error %v, want %v", err, lastErr)
	}
	if calls != 4 {
		t.Fatalf("Retry: fn called %d times, want 4 (1 initial + 3 retries)", calls)
	}
	want := []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second}
	if !durationsEqual(*delays, want) {
		t.Fatalf("Retry: sleep called with %v, want %v", *delays, want)
	}
}

func durationsEqual(got, want []time.Duration) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
