// Package mcpserver wraps the mcp-go SDK with typed tool registration,
// KD3 envelope formatting, and error classification.
package mcpserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
)

// --- Error types ---

// DomainError represents a business-logic violation (e.g. invalid input from
// the caller's perspective). Mapped to envelope code "domain".
type DomainError struct {
	Msg        string
	Suggestion string
	Cause      error
}

func (e *DomainError) Error() string { return e.Msg }
func (e *DomainError) Unwrap() error { return e.Cause }

// InfraError represents an infrastructure failure (network, filesystem, etc.).
// Mapped to envelope code "infra".
type InfraError struct {
	Msg        string
	Suggestion string
	Cause      error
}

func (e *InfraError) Error() string { return e.Msg }
func (e *InfraError) Unwrap() error { return e.Cause }

// DataError represents a data-layer problem (schema mismatch, parse failure).
// Mapped to envelope code "data".
type DataError struct {
	Msg        string
	Suggestion string
	Cause      error
}

func (e *DataError) Error() string { return e.Msg }
func (e *DataError) Unwrap() error { return e.Cause }

// mapError classifies an error into a KD3 code, message, and recovery
// suggestion. Wrapped errors are matched via errors.As. Unknown errors
// default to "infra" with no suggestion.
func mapError(err error) (code string, msg string, suggestion string) {
	var de *DomainError
	if errors.As(err, &de) {
		return "domain", err.Error(), de.Suggestion
	}
	var ie *InfraError
	if errors.As(err, &ie) {
		return "infra", err.Error(), ie.Suggestion
	}
	var dae *DataError
	if errors.As(err, &dae) {
		return "data", err.Error(), dae.Suggestion
	}
	// Unknown errors are infrastructure failures.
	return "infra", err.Error(), ""
}

// --- Markdown error rendering ---

// renderError renders a failed tool result as the Markdown text that becomes
// the tool's content[0].text. It reuses render.go's renderer for the same
// heading/blank-line/plain-block primitives renderOK uses, so success and
// error output share one visual style.
//
// The "Do this" section is never empty: when suggestion is "" (the error
// carried no caller-supplied recovery text), it falls back to
// defaultRecovery(code).
func renderError(tool, code, msg, suggestion string) string {
	r := &renderer{empty: true}
	r.line("# " + tool + " — error: " + code)

	r.blank()
	r.heading(2, "What happened")
	if msg == "" {
		r.line(renderNone)
	} else {
		r.writeBlock(msg)
	}

	if suggestion == "" {
		suggestion = defaultRecovery(code)
	}
	r.blank()
	r.heading(2, "Do this")
	r.writeBlock(suggestion)

	return r.b.String()
}

// defaultRecovery returns generic recovery guidance for a KD3 error code,
// used by renderError when the error itself carries no suggestion. A code
// other than "domain" or "data" (including "infra" and any code this
// function doesn't recognize) gets the infra text: an unclassified failure
// is more likely a plumbing problem than a caller mistake.
func defaultRecovery(code string) string {
	switch code {
	case "domain":
		return "Check the input against this tool's documented parameters and retry with a corrected value."
	case "data":
		return "The underlying data may be missing, malformed, or stale. Inspect the referenced file or record, regenerate it if needed, and retry."
	default:
		return "This looks like an environment or infrastructure failure (filesystem, network, or process). Check that the underlying system is reachable and retry."
	}
}

// --- KD3 envelope ---

// OKEnvelope is the success envelope shape published via WithOutputSchema.
// It mirrors the runtime envelope that wrapOK produces, giving callers a
// machine-readable output schema: {"ok":true,"data":<TOut>}.
type OKEnvelope[T any] struct {
	OK   bool `json:"ok"`
	Data T    `json:"data"`
}

type envelopeOK struct {
	OK   bool            `json:"ok"`
	Data json.RawMessage `json:"data"`
}

type envelopeErr struct {
	OK         bool   `json:"ok"`
	Code       string `json:"code"`
	Error      string `json:"error"`
	Suggestion string `json:"suggestion,omitempty"`
}

// wrapOK marshals data into a KD3 success envelope: {"ok":true,"data":...}.
//
// TODO: nil slices/maps in the input struct serialize as JSON null instead of
// []/{}. Callers should initialize slice fields to empty (e.g. []string{})
// rather than leaving them nil. A central normalization pass here would be
// the definitive fix but requires reflection; see review finding #12.
func wrapOK(data any) ([]byte, error) {
	raw, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("marshal data: %w", err)
	}
	return json.Marshal(envelopeOK{OK: true, Data: raw})
}

// wrapErr builds a KD3 error envelope: {"ok":false,"code":"...","error":"..."}.
// suggestion is omitted from the JSON when empty.
func wrapErr(code, msg, suggestion string) ([]byte, error) {
	return json.Marshal(envelopeErr{OK: false, Code: code, Error: msg, Suggestion: suggestion})
}

// --- Dedup ---

// Dedup tracks seen warning messages and deduplicates them.
// Safe for use within a single goroutine (one per tool call).
type Dedup struct {
	mu   sync.Mutex
	seen map[string]bool
}

// NewDedup creates a new Dedup instance.
func NewDedup() *Dedup {
	return &Dedup{seen: make(map[string]bool)}
}

// Add records msg. Returns true if this is the first time msg was added.
func (d *Dedup) Add(msg string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.seen[msg] {
		return false
	}
	d.seen[msg] = true
	return true
}
