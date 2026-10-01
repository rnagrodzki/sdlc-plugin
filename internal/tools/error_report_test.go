package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
)

// ---------------------------------------------------------------------------
// Required fields
// ---------------------------------------------------------------------------

func TestErrorReportPrepare_MissingRequiredFields(t *testing.T) {
	root := t.TempDir()
	_, err := errorReportPrepare(root, ErrorReportPrepareIn{})
	if err == nil {
		t.Fatal("expected a missing-required-field error, got nil")
	}
	domainErr, ok := err.(*mcpserver.DomainError)
	if !ok {
		t.Fatalf("expected *mcpserver.DomainError, got %T: %v", err, err)
	}
	for _, field := range []string{"skill", "step", "operation", "errorText"} {
		if !containsSubstr(domainErr.Msg, field) {
			t.Errorf("expected message to mention missing field %q, got: %s", field, domainErr.Msg)
		}
	}
	// The suggestion must name the live tool, not the retired
	// error_report_prepare.
	if !containsSubstr(domainErr.Suggestion, "prepare_orchestrator") || containsSubstr(domainErr.Suggestion, "error_report_prepare") {
		t.Errorf("suggestion should point at prepare_orchestrator, got: %s", domainErr.Suggestion)
	}
}

func TestErrorReportPrepare_PartialFieldsStillReportsRemainingMissing(t *testing.T) {
	root := t.TempDir()
	_, err := errorReportPrepare(root, ErrorReportPrepareIn{
		Skill: "ship",
		Step:  "step-1",
	})
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	domainErr, ok := err.(*mcpserver.DomainError)
	if !ok {
		t.Fatalf("expected *mcpserver.DomainError, got %T: %v", err, err)
	}
	if containsSubstr(domainErr.Msg, "Missing required field: skill") {
		t.Errorf("skill was supplied, should not be reported missing: %s", domainErr.Msg)
	}
	if !containsSubstr(domainErr.Msg, "operation") || !containsSubstr(domainErr.Msg, "errorText") {
		t.Errorf("expected operation and errorText reported missing, got: %s", domainErr.Msg)
	}
}

// ---------------------------------------------------------------------------
// Manifest field fidelity
// ---------------------------------------------------------------------------

func TestErrorReportPrepare_ManifestFieldFidelity(t *testing.T) {
	root := t.TempDir()

	out, err := errorReportPrepare(root, ErrorReportPrepareIn{
		Skill:                  "  ship  ",
		Step:                   "  step-1  ",
		Operation:              "  do-thing  ",
		Error:                  "  boom happened  ",
		ExitOrHTTPCode:         "  1  ",
		ErrorType:              "  timeout  ",
		UserIntent:             "  fix it please  ",
		ArgsString:             "  --flag value  ",
		SuggestedInvestigation: "  check the logs  ",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.ManifestPath == "" {
		t.Fatal("expected a non-empty ManifestPath")
	}

	manifest := readErrorReportManifest(t, out.ManifestPath)

	if manifest["skill"] != "ship" {
		t.Errorf("skill = %q, want trimmed", manifest["skill"])
	}
	if manifest["step"] != "step-1" {
		t.Errorf("step = %q, want trimmed", manifest["step"])
	}
	if manifest["operation"] != "do-thing" {
		t.Errorf("operation = %q, want trimmed", manifest["operation"])
	}
	if manifest["errorText"] != "  boom happened  " {
		t.Errorf("errorText = %q, want raw (untrimmed) value", manifest["errorText"])
	}
	if manifest["exitOrHttpCode"] != "  1  " {
		t.Errorf("exitOrHttpCode = %q, want raw (untrimmed) value", manifest["exitOrHttpCode"])
	}
	if manifest["errorType"] != "timeout" {
		t.Errorf("errorType = %q, want trimmed", manifest["errorType"])
	}
	if manifest["userIntent"] != "  fix it please  " {
		t.Errorf("userIntent = %q, want raw (untrimmed) value", manifest["userIntent"])
	}
	if manifest["argsString"] != "  --flag value  " {
		t.Errorf("argsString = %q, want raw (untrimmed) value", manifest["argsString"])
	}
	if manifest["suggestedInvestigation"] != "  check the logs  " {
		t.Errorf("suggestedInvestigation = %q, want raw (untrimmed) value", manifest["suggestedInvestigation"])
	}
	if manifest["targetRepo"] != errorReportTargetRepo {
		t.Errorf("targetRepo = %v, want %q", manifest["targetRepo"], errorReportTargetRepo)
	}

	labels, ok := manifest["labels"].([]any)
	if !ok || len(labels) != 2 {
		t.Fatalf("labels = %+v, want [tooling-error, ship]", manifest["labels"])
	}
	if labels[0] != "tooling-error" || labels[1] != "ship" {
		t.Errorf("labels = %+v, want [tooling-error, ship] (trimmed skill)", labels)
	}

	if _, ok := manifest["timestamp"].(string); !ok || manifest["timestamp"] == "" {
		t.Errorf("expected a non-empty timestamp string, got %v", manifest["timestamp"])
	}
}

// TestErrorReportPrepare_ResultIsManifestPathOnly pins errorReportPrepare's
// result to the manifest path. prepare_orchestrator forwards only
// manifestPath (plus its own mode), and the orchestrator agent reads every
// other value from the manifest file, so mirrored fields would be dead.
func TestErrorReportPrepare_ResultIsManifestPathOnly(t *testing.T) {
	out, err := errorReportPrepare(t.TempDir(), ErrorReportPrepareIn{
		Skill:     "ship",
		Step:      "step-1",
		Operation: "do-thing",
		Error:     "boom",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// errorReportPrepare writes its manifest dir with os.MkdirTemp directly
	// (not the fsseam), so remove it here.
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Dir(out.ManifestPath)) })
	data, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	if len(m) != 1 || m["manifestPath"] == nil || m["manifestPath"] == "" {
		t.Errorf("result = %v, want only a non-empty manifestPath", m)
	}
}

func TestErrorReportPrepare_OptionalFieldsDefaultToEmptyString(t *testing.T) {
	root := t.TempDir()
	out, err := errorReportPrepare(root, ErrorReportPrepareIn{
		Skill:     "ship",
		Step:      "step-1",
		Operation: "do-thing",
		Error:     "boom",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	manifest := readErrorReportManifest(t, out.ManifestPath)
	for _, field := range []string{"exitOrHttpCode", "errorType", "userIntent", "argsString", "suggestedInvestigation"} {
		if manifest[field] != "" {
			t.Errorf("%s = %v, want empty-string default", field, manifest[field])
		}
	}
}

// ---------------------------------------------------------------------------
// RULING 6 / RULING 7: Contract completeness + wire field naming
// ---------------------------------------------------------------------------

func TestErrorReportPrepareIn_HasAllFiveOptionalFieldsWithNoSkipConfigCheck(t *testing.T) {
	typ := reflect.TypeOf(ErrorReportPrepareIn{})

	required := map[string]bool{
		"Skill": true, "Step": true, "Operation": true, "Error": true,
	}
	optional := map[string]bool{
		"ExitOrHTTPCode": true, "ErrorType": true, "UserIntent": true,
		"ArgsString": true, "SuggestedInvestigation": true,
	}

	seen := map[string]reflect.StructField{}
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		seen[f.Name] = f
		if f.Name == "SkipConfigCheck" {
			t.Fatal("ErrorReportPrepareIn must NOT have a SkipConfigCheck field — source has no config-version gate")
		}
	}

	for name := range required {
		if _, ok := seen[name]; !ok {
			t.Errorf("missing required field %q on ErrorReportPrepareIn", name)
		}
	}
	for name := range optional {
		if _, ok := seen[name]; !ok {
			t.Errorf("missing optional field %q on ErrorReportPrepareIn (RULING 6: all five optional fields must be present)", name)
		}
	}

	// RULING 7: Error carries the wire tag "errorText" (matches source's
	// field name), not "error".
	errField := seen["Error"]
	tag := errField.Tag.Get("json")
	if tag != "errorText" {
		t.Errorf("ErrorReportPrepareIn.Error json tag = %q, want \"errorText\" (RULING 7)", tag)
	}
}

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

func readErrorReportManifest(t *testing.T, path string) map[string]any {
	t.Helper()
	if path == "" {
		t.Fatal("manifest path is empty")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal manifest: %v", err)
	}
	return m
}

// TestPrepareOrchestrator_ErrorReportManifestCarriesTemplate pins that the
// error_report manifest carries the ToolingError.md template text itself.
// The error-report-orchestrator agent runs with the user's project as its
// working directory, where the plugin's skills/ tree does not exist, so it
// must get the template from the manifest, not from a project-relative path.
func TestPrepareOrchestrator_ErrorReportManifestCarriesTemplate(t *testing.T) {
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

	want, err := os.ReadFile(filepath.Join("..", "..", "plugins", "sdlc", "skills", "error-report", "templates", "ToolingError.md"))
	if err != nil {
		t.Fatalf("read shipped template: %v", err)
	}
	manifest := readErrorReportManifest(t, out.ManifestPath)
	got, _ := manifest["template"].(string)
	if got != string(want) {
		t.Fatalf("manifest template = %q, want the shipped ToolingError.md content", got)
	}
}
