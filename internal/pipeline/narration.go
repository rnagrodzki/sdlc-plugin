package pipeline

// TimingInfo carries optional duration/idle figures attached to a
// Narration payload. Every field is omitempty: an unknown timing must be
// left absent from the JSON, never rendered as a misleading zero.
type TimingInfo struct {
	StepSeconds     int    `json:"stepSeconds,omitempty"`
	PipelineSeconds int    `json:"pipelineSeconds,omitempty"`
	IdleSeconds     int    `json:"idleSeconds,omitempty"`
	Human           string `json:"human,omitempty"`
}

// NextAction describes what the pipeline will do next, surfaced to the
// user alongside a Narration so a resumed or observing session knows what
// is about to happen without re-deriving it.
type NextAction struct {
	ID          string `json:"id"`
	Instruction string `json:"instruction"`
	EtaSeconds  int    `json:"etaSeconds,omitempty"`
	EtaBasis    string `json:"etaBasis,omitempty"`
}

// Narration is the data payload embedded inside the existing KD3 envelope
// returned by MCP tool responses (execute_state, ship_state, etc). The KD3
// envelope wrapping itself is untouched by this package — Narration only
// supplies the payload that goes inside it.
//
// Display is authoritative for user-facing rendering: callers (and
// SKILL.md instructions) render Display verbatim. Summary is a shorter
// form intended for speech / Key Decisions logs, not for markdown
// rendering.
type Narration struct {
	Summary string      `json:"summary"`
	Display string      `json:"display"`
	Timing  *TimingInfo `json:"timing,omitempty"`
	Next    *NextAction `json:"next,omitempty"`
}

// StateIssue is a structured issue-accumulator entry. It lives here, in
// pipeline, because internal/tools cannot be the canonical owner: tools
// imports pipeline (for Narration embedding), so the reverse import would
// cycle. internal/tools.StateIssue (execute_state.go) is a type alias of
// this type (`type StateIssue = pipeline.StateIssue`), not a separate
// duplicate — tools code can construct/consume issues via its own
// StateIssue name with zero conversion.
type StateIssue struct {
	Wave      int    `json:"wave,omitempty"`
	Step      string `json:"step,omitempty"`
	TaskID    string `json:"taskId,omitempty"`
	Severity  string `json:"severity"`
	Category  string `json:"category"`
	Summary   string `json:"summary"`
	Detail    string `json:"detail,omitempty"`
	Timestamp string `json:"timestamp"`
}
