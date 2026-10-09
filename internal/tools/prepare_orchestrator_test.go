package tools

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
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
	if !reflect.DeepEqual(out, PrepareOrchestratorOut{}) {
		t.Fatalf("expected zero-value output on error, got %#v", out)
	}
}

// ---------------------------------------------------------------------------
// Output: customInstructions and next
// ---------------------------------------------------------------------------

// newOrchestratorRepo creates a temporary git repository whose
// .sdlc-v2/config.toml holds configToml, makes it the working directory for
// the test and returns its path. prepareOrchestrator resolves the project
// root from the working directory, so the test needs a repository of its own.
func newOrchestratorRepo(t *testing.T, configToml string) string {
	t.Helper()
	dir := t.TempDir()
	if out, err := exec.Command("git", "-C", dir, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	writeFile(t, filepath.Join(dir, paths.DataDir, "config.toml"), configToml)
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(dir))
	t.Chdir(dir)
	return dir
}

// orchestratorOutputJSON marshals out the way the MCP server does and decodes
// it into a generic object, so a test sees the field names and omissions of
// the wire format.
func orchestratorOutputJSON(t *testing.T, out PrepareOrchestratorOut) map[string]any {
	t.Helper()
	raw, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal output: %v", err)
	}
	var wire map[string]any
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	return wire
}

// TestPrepareOrchestrator_HardenReturnsInstructionsAndNext asserts that harden
// mode returns the configured instruction lists and the next step, and that
// the manifest file at manifestPath carries the same map.
func TestPrepareOrchestrator_HardenReturnsInstructionsAndNext(t *testing.T) {
	newOrchestratorRepo(t, hardenInstructionsConfig)

	out, err := prepareOrchestrator(PrepareOrchestratorIn{
		Mode:            "harden",
		Skill:           "ship",
		FailureText:     "boom",
		SkipConfigCheck: true,
	})
	if err != nil {
		t.Fatalf("prepareOrchestrator: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Dir(out.ManifestPath)) })

	want := map[string][]string{
		"plan-guardrails":      {"Prefer a warning over an error.", "Name the failing task."},
		"execute-guardrails":   {},
		"review-dimensions":    {},
		"copilot-instructions": {"Keep each rule to one line."},
	}
	if !reflect.DeepEqual(out.CustomInstructions, want) {
		t.Errorf("CustomInstructions = %#v, want %#v", out.CustomInstructions, want)
	}
	if out.Next != prepareNextHarden {
		t.Errorf("Next = %q, want %q", out.Next, prepareNextHarden)
	}

	wire := orchestratorOutputJSON(t, out)
	if _, ok := wire["customInstructions"].(map[string]any); !ok {
		t.Errorf("wire customInstructions is not an object: %T", wire["customInstructions"])
	}
	if wire["next"] != prepareNextHarden {
		t.Errorf("wire next = %v, want %q", wire["next"], prepareNextHarden)
	}

	manifest := readHardenManifest(t, out.ManifestPath)
	if !reflect.DeepEqual(manifest["customInstructions"], wire["customInstructions"]) {
		t.Errorf("manifest customInstructions = %v, want the tool output map %v", manifest["customInstructions"], wire["customInstructions"])
	}
}

// TestPrepareOrchestrator_ErrorReportOmitsInstructions asserts that
// error_report mode returns next and leaves customInstructions out of the
// output.
func TestPrepareOrchestrator_ErrorReportOmitsInstructions(t *testing.T) {
	newOrchestratorRepo(t, hardenInstructionsConfig)

	out, err := prepareOrchestrator(PrepareOrchestratorIn{
		Mode:      "error_report",
		Skill:     "ship",
		Step:      "step-1",
		Operation: "do-thing",
		Error:     "boom",
	})
	if err != nil {
		t.Fatalf("prepareOrchestrator: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Dir(out.ManifestPath)) })

	if out.CustomInstructions != nil {
		t.Errorf("CustomInstructions = %#v, want nil in error_report mode", out.CustomInstructions)
	}
	wire := orchestratorOutputJSON(t, out)
	if _, present := wire["customInstructions"]; present {
		t.Errorf("wire output has customInstructions in error_report mode: %v", wire)
	}
	if wire["next"] != prepareNextErrorReport {
		t.Errorf("wire next = %v, want %q", wire["next"], prepareNextErrorReport)
	}
}

// TestPrepareOrchestrator_HardenInvalidInstructionsReturnsError asserts that
// an invalid [harden.instructions] list fails harden mode with the loader
// DomainError, which names the key, and returns no output.
func TestPrepareOrchestrator_HardenInvalidInstructionsReturnsError(t *testing.T) {
	newOrchestratorRepo(t, "[harden.instructions]\nplan-guardrails = \"not a list\"\n")

	out, err := prepareOrchestrator(PrepareOrchestratorIn{
		Mode:            "harden",
		Skill:           "ship",
		FailureText:     "boom",
		SkipConfigCheck: true,
	})
	if err == nil {
		t.Fatal("expected an error for an invalid instruction list, got nil")
	}
	var domainErr *mcpserver.DomainError
	if !errorsAsDomainError(err, &domainErr) {
		t.Fatalf("expected *mcpserver.DomainError, got %T: %v", err, err)
	}
	if !containsSubstr(domainErr.Msg, "harden.instructions.plan-guardrails") {
		t.Errorf("expected the message to name the key, got: %s", domainErr.Msg)
	}
	if out.ManifestPath != "" || out.Next != "" || out.CustomInstructions != nil {
		t.Errorf("expected zero-value output on error, got %#v", out)
	}
}
