package tools

import (
	"reflect"
	"testing"

	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
)

// ---------------------------------------------------------------------------
// Field-mapping fidelity: every field on HardenPrepareIn/ErrorReportPrepareIn
// must survive the round trip through PrepareOrchestratorIn -> toXPrepareIn.
// These are pure struct-literal tests, deliberately independent of any
// worktree/filesystem state, so they pin the exact concern the fact sheet
// flagged: dropping mode-specific fields (SkipConfigCheck, FromIssue,
// ExitOrHTTPCode, SuggestedInvestigation, ...) during the merge.
// ---------------------------------------------------------------------------

func TestToHardenPrepareIn_AllFieldsMapped(t *testing.T) {
	in := PrepareOrchestratorIn{
		Mode:            "harden",
		FailureText:     "boom",
		Skill:           "ship",
		Step:            "verify",
		Operation:       "build",
		ExitCode:        "1",
		ErrorType:       "CLI failure",
		UserIntent:      "release",
		ArgsString:      "--flag",
		FromIssue:       "42",
		SkipConfigCheck: true,

		// error_report-only fields must NOT leak into HardenPrepareIn.
		Error:                  "should not leak",
		ExitOrHTTPCode:         "500",
		SuggestedInvestigation: "should not leak",
	}

	got := toHardenPrepareIn(in)
	want := HardenPrepareIn{
		FailureText:     "boom",
		Skill:           "ship",
		Step:            "verify",
		Operation:       "build",
		ExitCode:        "1",
		ErrorType:       "CLI failure",
		UserIntent:      "release",
		ArgsString:      "--flag",
		FromIssue:       "42",
		SkipConfigCheck: true,
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("toHardenPrepareIn mismatch:\n got: %#v\nwant: %#v", got, want)
	}
}

func TestToErrorReportPrepareIn_AllFieldsMapped(t *testing.T) {
	in := PrepareOrchestratorIn{
		Mode:                   "error_report",
		Skill:                  "ship",
		Step:                   "verify",
		Operation:              "build",
		Error:                  "exit 1",
		ExitOrHTTPCode:         "500",
		ErrorType:              "CLI failure",
		UserIntent:             "release",
		ArgsString:             "--flag",
		SuggestedInvestigation: "check logs",

		// harden-only fields must NOT leak into ErrorReportPrepareIn.
		FailureText:     "should not leak",
		ExitCode:        "1",
		FromIssue:       "42",
		SkipConfigCheck: true,
	}

	got := toErrorReportPrepareIn(in)
	want := ErrorReportPrepareIn{
		Skill:                  "ship",
		Step:                   "verify",
		Operation:              "build",
		Error:                  "exit 1",
		ExitOrHTTPCode:         "500",
		ErrorType:              "CLI failure",
		UserIntent:             "release",
		ArgsString:             "--flag",
		SuggestedInvestigation: "check logs",
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("toErrorReportPrepareIn mismatch:\n got: %#v\nwant: %#v", got, want)
	}
}

// TestPrepareOrchestratorIn_ExitCodeFieldsAreIndependent guards against the
// two exit-code-shaped fields (harden's ExitCode, error_report's
// ExitOrHTTPCode) ever being collapsed into a single field — they carry
// separate JSON tags and separate meanings per mode.
func TestPrepareOrchestratorIn_ExitCodeFieldsAreIndependent(t *testing.T) {
	in := PrepareOrchestratorIn{ExitCode: "1", ExitOrHTTPCode: "500"}
	if in.ExitCode == in.ExitOrHTTPCode {
		t.Fatalf("ExitCode and ExitOrHTTPCode collapsed to the same value: %q", in.ExitCode)
	}

	hardenOut := toHardenPrepareIn(in)
	if hardenOut.ExitCode != "1" {
		t.Errorf("toHardenPrepareIn: ExitCode = %q, want %q", hardenOut.ExitCode, "1")
	}

	errOut := toErrorReportPrepareIn(in)
	if errOut.ExitOrHTTPCode != "500" {
		t.Errorf("toErrorReportPrepareIn: ExitOrHTTPCode = %q, want %q", errOut.ExitOrHTTPCode, "500")
	}
}

// ---------------------------------------------------------------------------
// Mode dispatch
// ---------------------------------------------------------------------------

func TestPrepareOrchestrator_UnknownModeReturnsDomainError(t *testing.T) {
	_, err := prepareOrchestrator(PrepareOrchestratorIn{Mode: "bogus"})
	if err == nil {
		t.Fatal("expected an error for an unrecognized mode, got nil")
	}
	var domainErr *mcpserver.DomainError
	if !errorsAsDomainError(err, &domainErr) {
		t.Fatalf("expected *mcpserver.DomainError, got %T: %v", err, err)
	}
}

func TestPrepareOrchestrator_MissingModeReturnsDomainError(t *testing.T) {
	_, err := prepareOrchestrator(PrepareOrchestratorIn{})
	if err == nil {
		t.Fatal("expected an error for a missing mode, got nil")
	}
	var domainErr *mcpserver.DomainError
	if !errorsAsDomainError(err, &domainErr) {
		t.Fatalf("expected *mcpserver.DomainError, got %T: %v", err, err)
	}
}

// TestPrepareOrchestrator_UnknownModeDoesNotResolveWorktree confirms mode
// validation happens before any worktree/root resolution: an unrecognized
// mode must fail with a DomainError even when run somewhere worktree.MainRoot
// would otherwise fail (or succeed) -- the two are independent, and this
// test would flake if dispatch order were reversed (mode-check after root
// resolution) in an environment where MainRoot errors first.
func TestPrepareOrchestrator_UnknownModeDoesNotResolveWorktree(t *testing.T) {
	out, err := prepareOrchestrator(PrepareOrchestratorIn{Mode: "not-a-real-mode"})
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if out != (PrepareOrchestratorOut{}) {
		t.Fatalf("expected zero-value output on error, got %#v", out)
	}
}
