package mcpserver

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestWrapErr_WithSuggestion(t *testing.T) {
	raw, err := wrapErr("domain", "releaseLevel is empty", "Run /version to set release intent.")
	if err != nil {
		t.Fatalf("wrapErr returned error: %v", err)
	}
	if !strings.Contains(string(raw), `"suggestion":"Run /version to set release intent."`) {
		t.Fatalf("expected suggestion in envelope JSON, got: %s", raw)
	}

	var got envelopeErr
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	if got.Suggestion != "Run /version to set release intent." {
		t.Errorf("Suggestion = %q, want %q", got.Suggestion, "Run /version to set release intent.")
	}
}

func TestWrapErr_WithoutSuggestion(t *testing.T) {
	raw, err := wrapErr("infra", "something failed", "")
	if err != nil {
		t.Fatalf("wrapErr returned error: %v", err)
	}
	if strings.Contains(string(raw), "suggestion") {
		t.Fatalf("expected no suggestion key in envelope JSON (omitempty), got: %s", raw)
	}
}

func TestMapError_ExtractsSuggestion(t *testing.T) {
	tests := []struct {
		name           string
		err            error
		wantCode       string
		wantSuggestion string
	}{
		{
			name:           "domain error with suggestion",
			err:            &DomainError{Msg: "bad input", Suggestion: "fix the input"},
			wantCode:       "domain",
			wantSuggestion: "fix the input",
		},
		{
			name:           "infra error with suggestion",
			err:            &InfraError{Msg: "network down", Suggestion: "retry the request"},
			wantCode:       "infra",
			wantSuggestion: "retry the request",
		},
		{
			name:           "data error with suggestion",
			err:            &DataError{Msg: "bad schema", Suggestion: "regenerate the file"},
			wantCode:       "data",
			wantSuggestion: "regenerate the file",
		},
		{
			name:           "unknown error has no suggestion",
			err:            errUnknown("boom"),
			wantCode:       "infra",
			wantSuggestion: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, msg, suggestion := mapError(tt.err)
			if code != tt.wantCode {
				t.Errorf("code = %q, want %q", code, tt.wantCode)
			}
			if msg != tt.err.Error() {
				t.Errorf("msg = %q, want %q", msg, tt.err.Error())
			}
			if suggestion != tt.wantSuggestion {
				t.Errorf("suggestion = %q, want %q", suggestion, tt.wantSuggestion)
			}
		})
	}
}

// errUnknown is a plain error type not recognized by mapError, used to
// exercise the default "infra" fallback path.
type errUnknown string

func (e errUnknown) Error() string { return string(e) }

func TestRenderError_ExactShape(t *testing.T) {
	got := renderError("mytool", "domain", "bad input", "fix the input")
	want := "# mytool — error: domain\n\n## What happened\nbad input\n\n## Do this\nfix the input\n"
	if got != want {
		t.Errorf("renderError() =\n%q\nwant\n%q", got, want)
	}
}

func TestRenderError_EmptySuggestionUsesDefaultRecovery(t *testing.T) {
	got := renderError("mytool", "data", "schema mismatch", "")
	want := "# mytool — error: data\n\n## What happened\nschema mismatch\n\n## Do this\n" + defaultRecovery("data") + "\n"
	if got != want {
		t.Errorf("renderError() =\n%q\nwant\n%q", got, want)
	}
	if !strings.Contains(got, "## Do this") {
		t.Fatalf("expected a non-empty Do this section, got: %s", got)
	}
}

func TestRenderError_EmptyMsgRendersNone(t *testing.T) {
	got := renderError("mytool", "infra", "", "retry later")
	if !strings.Contains(got, "## What happened\n"+renderNone+"\n") {
		t.Errorf("expected empty msg to render %q, got: %s", renderNone, got)
	}
}

func TestRenderError_MultilineTextIsNotFenced(t *testing.T) {
	got := renderError("mytool", "infra", "line one\nline two", "step one\nstep two")
	if strings.Contains(got, "```") {
		t.Errorf("expected multi-line error text to render as plain lines, not fenced: %s", got)
	}
	if !strings.Contains(got, "line one\nline two") {
		t.Errorf("expected msg lines verbatim, got: %s", got)
	}
	if !strings.Contains(got, "step one\nstep two") {
		t.Errorf("expected suggestion lines verbatim, got: %s", got)
	}
}

func TestDefaultRecovery(t *testing.T) {
	tests := []struct {
		code string
		want string
	}{
		{"domain", "Check the input against this tool's documented parameters and retry with a corrected value."},
		{"data", "The underlying data may be missing, malformed, or stale. Inspect the referenced file or record, regenerate it if needed, and retry."},
		{"infra", "This looks like an environment or infrastructure failure (filesystem, network, or process). Check that the underlying system is reachable and retry."},
	}
	for _, tt := range tests {
		t.Run(tt.code, func(t *testing.T) {
			if got := defaultRecovery(tt.code); got != tt.want {
				t.Errorf("defaultRecovery(%q) = %q, want %q", tt.code, got, tt.want)
			}
		})
	}

	// An unrecognized code falls back to the same text as "infra" (rule:
	// "other falls back to infra text").
	if got, want := defaultRecovery("something-unrecognized"), defaultRecovery("infra"); got != want {
		t.Errorf("defaultRecovery(unrecognized) = %q, want same as infra %q", got, want)
	}

	// defaultRecovery never returns an empty string, so renderError's "Do
	// this" section is never empty.
	for _, code := range []string{"domain", "infra", "data", "", "other"} {
		if defaultRecovery(code) == "" {
			t.Errorf("defaultRecovery(%q) returned empty string", code)
		}
	}
}
