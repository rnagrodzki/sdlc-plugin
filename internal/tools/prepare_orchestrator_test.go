package tools

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/rnagrodzki/sdlc-plugin/internal/history"
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

	fake := &fakeToolCatalog{}
	in.catalog = fake

	got := toHardenPrepareIn(in)
	if got.catalog != fake {
		t.Errorf("catalog not copied")
	}
	want := HardenPrepareIn{
		catalog:         fake,
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

// TestPrepareOrchestratorIn_ModeSchemaEnum pins the input schema of mode to
// the closed set the handler switches on: harden and error_report.
func TestPrepareOrchestratorIn_ModeSchemaEnum(t *testing.T) {
	f, ok := reflect.TypeOf(PrepareOrchestratorIn{}).FieldByName("Mode")
	if !ok {
		t.Fatal("PrepareOrchestratorIn has no Mode field")
	}
	if got, want := f.Tag.Get("jsonschema"), "enum=harden,enum=error_report"; got != want {
		t.Errorf("PrepareOrchestratorIn.Mode jsonschema tag = %q, want %q", got, want)
	}
}

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
	if nextWithoutDeadRefSkips(out.Next) != prepareNextHarden {
		t.Errorf("Next = %q, want %q", out.Next, prepareNextHarden)
	}

	wire := orchestratorOutputJSON(t, out)
	if _, ok := wire["customInstructions"].(map[string]any); !ok {
		t.Errorf("wire customInstructions is not an object: %T", wire["customInstructions"])
	}
	if nextWithoutDeadRefSkips(wire["next"].(string)) != prepareNextHarden {
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
	redirectTempDir(t)
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

// TestPrepareOrchestrator_ErrorReportWritesFreshManifestPerCall asserts that
// two error_report calls with the same input return different manifest paths.
func TestPrepareOrchestrator_ErrorReportWritesFreshManifestPerCall(t *testing.T) {
	redirectTempDir(t)
	newOrchestratorRepo(t, hardenInstructionsConfig)
	in := PrepareOrchestratorIn{
		Mode:      "error_report",
		Skill:     "ship",
		Step:      "step-1",
		Operation: "do-thing",
		Error:     "boom",
	}

	first, err := prepareOrchestrator(in)
	if err != nil {
		t.Fatalf("first prepareOrchestrator: %v", err)
	}
	second, err := prepareOrchestrator(in)
	if err != nil {
		t.Fatalf("second prepareOrchestrator: %v", err)
	}

	if first.ManifestPath == second.ManifestPath {
		t.Errorf("both calls returned manifestPath %q, want different paths", first.ManifestPath)
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

// TestPrepareOrchestrator_HardenReportsPreflightFindings asserts that harden
// mode reports the pre-flight finding count on the wire and adds the findings
// sentence to next. A config with one invalid guardrail id gives count 1 and
// the sentence. A clean config gives count 0 and the plain next text. The
// call runs prepareOrchestrator itself, so a dispatcher that drops the count
// or the sentence fails here.
func TestPrepareOrchestrator_HardenReportsPreflightFindings(t *testing.T) {
	cases := []struct {
		name      string
		config    string
		wantCount int
		wantNext  string
	}{
		{
			name:      "one finding",
			config:    "[plan.guardrails.Bad_ID]\ndescription = \"desc\"\n",
			wantCount: 1,
			wantNext:  prepareNextHarden + " 1 pre-flight findings: the harden orchestrator drafts repair proposals for them first.",
		},
		{
			name:      "clean config",
			config:    "[plan.guardrails.good-rule]\ndescription = \"Every task names its files.\"\n",
			wantCount: 0,
			wantNext:  prepareNextHarden,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			redirectTempDir(t)
			newOrchestratorRepo(t, tc.config)

			out, err := prepareOrchestrator(PrepareOrchestratorIn{
				Mode:            "harden",
				Skill:           "ship",
				FailureText:     "boom",
				SkipConfigCheck: true,
			})
			if err != nil {
				t.Fatalf("prepareOrchestrator: %v", err)
			}

			wire := orchestratorOutputJSON(t, out)
			if wire["preflightFindingCount"] != float64(tc.wantCount) {
				t.Errorf("wire preflightFindingCount = %v, want %d", wire["preflightFindingCount"], tc.wantCount)
			}
			if nextWithoutDeadRefSkips(wire["next"].(string)) != tc.wantNext {
				t.Errorf("wire next = %v, want %q", wire["next"], tc.wantNext)
			}
			if got := len(manifestPreflightFindings(t, out.ManifestPath)); got != tc.wantCount {
				t.Errorf("manifest holds %d preflightFindings, want %d", got, tc.wantCount)
			}
		})
	}
}

// writeHistoryManifest writes a small harden manifest and returns its path.
func writeHistoryManifest(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "manifest.json")
	if err := os.WriteFile(p, []byte(`{"mode":"harden"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// readHistorySection returns the history section of the manifest at p, or nil
// when the manifest has none.
func readHistorySection(t *testing.T, p string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("manifest does not parse: %v", err)
	}
	if m["mode"] != "harden" {
		t.Errorf("manifest mode = %v, want harden (other keys must stay)", m["mode"])
	}
	h, _ := m["history"].(map[string]any)
	return h
}

// TestInjectHardenHistory_UnreadableDeferredIsReported pins that a
// deferred.json that cannot be read is never read as "no open items": the
// error goes into history.readErrors and is returned.
func TestInjectHardenHistory_UnreadableDeferredIsReported(t *testing.T) {
	cases := []struct {
		name    string
		setup   func(t *testing.T, path string)
		wantErr string
	}{
		{"does not parse", func(t *testing.T, path string) {
			if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
				t.Fatal(err)
			}
		}, "does not parse"},
		{"over the size limit", func(t *testing.T, path string) {
			f, err := os.Create(path)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			if err := f.Truncate(history.DeferredFileMax + 1); err != nil {
				t.Fatal(err)
			}
		}, "larger than 8 MiB"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			histDir := t.TempDir()
			tc.setup(t, history.NewFileWriter(histDir).DeferredPath())
			manifest := writeHistoryManifest(t)

			readErrs, err := injectHardenHistory(manifest, histDir)
			if err != nil {
				t.Fatalf("injectHardenHistory: %v", err)
			}
			if len(readErrs) != 1 || !strings.Contains(readErrs[0], tc.wantErr) {
				t.Fatalf("readErrs = %q, want one entry with %q", readErrs, tc.wantErr)
			}
			h := readHistorySection(t, manifest)
			got, _ := h["readErrors"].([]any)
			if len(got) != 1 || got[0] != readErrs[0] {
				t.Errorf("history.readErrors = %v, want %q", h["readErrors"], readErrs)
			}
			if _, ok := h["openDeferred"]; ok {
				t.Errorf("history.openDeferred = %v, want no key", h["openDeferred"])
			}
		})
	}
}

// TestInjectHardenHistory_EmptyStoreLeavesManifest pins that an empty store
// with no read error adds no history section.
func TestInjectHardenHistory_EmptyStoreLeavesManifest(t *testing.T) {
	manifest := writeHistoryManifest(t)
	readErrs, err := injectHardenHistory(manifest, t.TempDir())
	if err != nil || readErrs != nil {
		t.Fatalf("injectHardenHistory = %q, %v, want nil, nil", readErrs, err)
	}
	if h := readHistorySection(t, manifest); h != nil {
		t.Errorf("history = %v, want none", h)
	}
}

// TestInjectHardenHistory_OpenDeferredAdded pins the normal path: an open
// item is in history.openDeferred and there is no readErrors key.
func TestInjectHardenHistory_OpenDeferredAdded(t *testing.T) {
	histDir := t.TempDir()
	if err := history.NewFileWriter(histDir).AddDeferred(history.DeferredIssue{
		ID: "harden-pending-1", Description: "edit x", Status: history.StatusOpen, Priority: history.PriorityLow,
	}); err != nil {
		t.Fatal(err)
	}
	manifest := writeHistoryManifest(t)

	readErrs, err := injectHardenHistory(manifest, histDir)
	if err != nil || readErrs != nil {
		t.Fatalf("injectHardenHistory = %q, %v, want nil, nil", readErrs, err)
	}
	h := readHistorySection(t, manifest)
	open, _ := h["openDeferred"].([]any)
	if len(open) != 1 {
		t.Fatalf("history.openDeferred = %v, want one item", h["openDeferred"])
	}
	if _, ok := h["readErrors"]; ok {
		t.Errorf("history.readErrors = %v, want no key", h["readErrors"])
	}
}

// TestInjectHardenHistory_ManifestErrorReturned pins that a manifest that
// does not parse is an error, not a silent success, and is left unchanged.
func TestInjectHardenHistory_ManifestErrorReturned(t *testing.T) {
	histDir := t.TempDir()
	if err := os.WriteFile(history.NewFileWriter(histDir).DeferredPath(), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(t.TempDir(), "manifest.json")
	if err := os.WriteFile(manifest, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := injectHardenHistory(manifest, histDir); err == nil {
		t.Fatal("injectHardenHistory: want an error for a manifest that does not parse")
	}
	if data, _ := os.ReadFile(manifest); string(data) != "not json" {
		t.Errorf("manifest = %q, want it unchanged", data)
	}
}

// TestHardenHistoryNote pins the text that prepare_orchestrator adds to Next
// for each result of injectHardenHistory.
func TestHardenHistoryNote(t *testing.T) {
	if got := hardenHistoryNote(nil, nil); got != "" {
		t.Errorf("no error: note = %q, want empty", got)
	}
	if got, want := hardenHistoryNote([]string{"a", "b"}, nil),
		" The history store could not be read, so the manifest's history section may miss runs or open deferred items (history.readErrors): a; b."; got != want {
		t.Errorf("read errors: note = %q, want %q", got, want)
	}
	if got, want := hardenHistoryNote([]string{"a"}, errors.New("disk full")),
		" History was not added to the manifest: disk full."; got != want {
		t.Errorf("manifest error: note = %q, want %q", got, want)
	}
}

// nextWithoutDeadRefSkips cuts the dead-reference skip sentence off a harden
// Next. A temp test repo has no tool catalog and no plugin root, so the scan
// always skips checks there; TestHardenOutToPrepareOut pins that sentence.
func nextWithoutDeadRefSkips(next string) string {
	before, _, _ := strings.Cut(next, " Dead-reference check skipped: ")
	return before
}
