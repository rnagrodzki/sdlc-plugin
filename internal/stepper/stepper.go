// Package stepper is the Go port of scripts/lib/stepper.js: a shared
// step-emitter utility for SDLC skill scripts/tools.
//
// The JS source's own doc comment describes the underlying protocol this
// package preserves:
//
//	Provides the universal two-call protocol: scripts emit one step at a
//	time, the LLM executes each step, and calls the script again with the
//	result. The script controls workflow sequencing; the LLM provides
//	domain knowledge.
//
// Two things are ported here:
//
//  1. Envelope — the frozen `{status, step, llm_decision, state_file,
//     progress, ext}` JSON shape from stepper.js::createEnvelope
//     (scripts/lib/stepper.js:1-30 and createEnvelope below it). Task 46's
//     skills loop (Wave 19) branches on these key names literally, so they
//     must never be renamed or restructured.
//  2. Bounded polling (KD8) — a resumable, non-blocking alternative to the
//     JS polling scripts' internal Atomics.wait sleep loop. See PollState.
package stepper

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/fsx"
)

// ---------------------------------------------------------------------------
// Envelope
// ---------------------------------------------------------------------------

// Envelope is the step-emitter payload. Field names and JSON keys mirror
// scripts/lib/stepper.js::createEnvelope verbatim: status, step,
// llm_decision, state_file, progress, ext, error. Do not rename these keys
// — Task 46's skill prose depends on them by name.
//
// The JS source's JSDoc documents status as "step"|"done"|"error". This Go
// port adds "pending" as a fourth value: the JS polling scripts block
// synchronously inside one process invocation until a verdict is reached,
// but KD8 bounded polling (this task) makes each Go tool call perform
// exactly one non-blocking probe and return immediately, so a fourth status
// is needed to say "no verdict yet, call again with state_file" without an
// internal sleep loop. This extends the value enum; it does not touch any
// key name.
type Envelope struct {
	Status      string         `json:"status"`
	Step        *string        `json:"step"`
	LLMDecision any            `json:"llm_decision"`
	StateFile   *string        `json:"state_file"`
	Progress    any            `json:"progress"`
	Ext         map[string]any `json:"ext"`
	// Error is only populated (and only marshaled, via omitempty) when
	// Status is "error" — mirroring createEnvelope's
	// `if (status === 'error' && options.error) envelope.error = ...`.
	Error string `json:"error,omitempty"`
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func newEnvelope(status, step, stateFile string, progress any, ext map[string]any, errMsg string) Envelope {
	if ext == nil {
		ext = map[string]any{}
	}
	e := Envelope{
		Status:    status,
		Step:      strPtr(step),
		StateFile: strPtr(stateFile),
		Progress:  progress,
		Ext:       ext,
	}
	if status == "error" && errMsg != "" {
		e.Error = errMsg
	}
	return e
}

// Pending builds a "pending" envelope: no verdict yet, resume from
// stateFile on the next call.
func Pending(stateFile string, progress any, ext map[string]any) Envelope {
	return newEnvelope("pending", "", stateFile, progress, ext, "")
}

// Done builds a terminal "done" envelope. stateFile may be empty or may
// reference the now-exhausted resume file (harmless to include — callers
// that don't need it simply ignore a non-null state_file on a done
// envelope).
func Done(stateFile, step string, ext map[string]any) Envelope {
	return newEnvelope("done", step, stateFile, nil, ext, "")
}

// NewError builds an "error" envelope carrying a classified failure message
// (e.g. a missing-gh-binary error from ghx.ErrGHNotFound) in the envelope's
// own error path, per this task's dependency note: "propagate that
// classification into the stepper envelope's error path rather than
// swallowing it." stateFile is preserved so the caller can still resume
// once the underlying problem (e.g. gh not installed) is fixed.
func NewError(stateFile, msg string) Envelope {
	return newEnvelope("error", "", stateFile, nil, nil, msg)
}

// ---------------------------------------------------------------------------
// KD8 bounded polling: resume-state files
// ---------------------------------------------------------------------------

// PollState is the resume state persisted between bounded-poll calls (KD8).
//
// KD8 bounded polling: each call to a polling tool performs exactly one
// non-blocking probe (e.g. one `gh` invocation) and returns immediately —
// it never sleeps. This is a deliberate divergence from the JS source
// (scripts/skill/await-remote-review.js, verify-pipeline.js), which block
// synchronously inside a single process invocation for up to `timeout`
// seconds via an internal Atomics.wait loop. A Go MCP tool call cannot
// block a caller for minutes the way a one-shot CLI script can, so the
// internal sleep loop is replaced by an external one: PollState tracks the
// wall-clock budget so a caller (Task 46's skills loop) can invoke the same
// tool repeatedly — once per external interval — passing the previous
// call's state_file back in, until the envelope reports a terminal status
// ("done" or "error").
type PollState struct {
	Skill           string `json:"skill"`
	StartedAt       int64  `json:"started_at"` // unix seconds
	TimeoutSeconds  int    `json:"timeout_seconds"`
	IntervalSeconds int    `json:"interval_seconds"`
	Iteration       int    `json:"iteration"`
	// Exhausted mirrors the JS source's per-script state-marker keys
	// (awaitRemoteReviewExhausted / verifyPipelineExhausted): once a poll
	// times out, the state file is marked exhausted so a later call
	// short-circuits to a "skipped" verdict instead of probing gh again.
	Exhausted bool `json:"exhausted"`
}

// NewPollState starts a fresh bounded-poll budget anchored at the current
// time.
func NewPollState(skill string, timeoutSeconds, intervalSeconds int) PollState {
	return PollState{
		Skill:           skill,
		StartedAt:       time.Now().Unix(),
		TimeoutSeconds:  timeoutSeconds,
		IntervalSeconds: intervalSeconds,
	}
}

// TimedOut reports whether the poll's timeout budget has elapsed.
func (s PollState) TimedOut() bool {
	return time.Since(time.Unix(s.StartedAt, 0)) >= time.Duration(s.TimeoutSeconds)*time.Second
}

// WaitedSeconds reports whole seconds elapsed since the poll started,
// mirroring the JS source's `waitedSeconds` field.
func (s PollState) WaitedSeconds() int {
	return int(time.Since(time.Unix(s.StartedAt, 0)).Seconds())
}

// NewStateFilePath returns a new resume-file path under os.TempDir(),
// mirroring scripts/lib/stepper.js::createStateFile's
// `path.join(os.tmpdir(), \`${skill}-${hash}.json\`)` convention.
//
// RULING: the task fact sheet's Notes describe the naming convention as
// "<skill>-<sha256>.json", but the JS source itself
// (crypto.randomBytes(6).toString('hex')) uses 6 random bytes hex-encoded,
// not a sha256 digest of anything. The fact sheet's own Contract says
// "stepper tmpdir convention retained" — retaining what the source
// actually does takes precedence over the fact sheet's paraphrase, so this
// port reproduces the random-bytes-hex suffix rather than a sha256 hash.
// Cost if wrong: cosmetic only (resume file name shape); nothing parses
// this filename's suffix as a hash.
func NewStateFilePath(skill string) (string, error) {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("stepper: generate state file name: %w", err)
	}
	name := fmt.Sprintf("%s-%s.json", skill, hex.EncodeToString(b))
	return filepath.Join(os.TempDir(), name), nil
}

// LoadPollState reads and parses a resume-state file written by
// SavePollState. It returns fsx's wrapped ErrNotFound/ErrParse errors
// unchanged so callers can decide whether to fail open (start fresh) or
// fail hard, matching the JS source's readStateFile, which always fails
// open (returns null on any read/parse error) — callers in this package
// choose to do the same.
func LoadPollState(path string) (PollState, error) {
	var s PollState
	err := fsx.ReadJSON(path, &s)
	return s, err
}

// SavePollState writes state to path atomically via fsx.AtomicWriteJSON.
func SavePollState(path string, state PollState) error {
	return fsx.AtomicWriteJSON(path, state)
}
