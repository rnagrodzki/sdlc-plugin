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
