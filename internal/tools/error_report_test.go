package tools

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/rnagrodzki/sdlc-plugin/internal/ghx"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
)

// init replaces the duplicate-issue search with a fake that finds no issues
// and reports no error, for every test of the package. A test that calls
// errorReportPrepare or prepareOrchestrator in error_report mode then makes no
// gh call and keeps its output. A test that needs matches or an error sets its
// own fake with stubErrorReportIssueList.
func init() {
	errorReportIssueList = func(dir, repo string, labels []string, limit int) ([]ghx.Issue, error) {
		return nil, nil
	}
}

// issueListCall records the arguments of one call to the fake issue search.
type issueListCall struct {
	dir    string
	repo   string
	labels []string
	limit  int
}

// stubErrorReportIssueList replaces errorReportIssueList with a fake that
// records each call and returns issues and err. It restores the previous
// function when the test ends. The returned slice grows by one entry for each
// call.
func stubErrorReportIssueList(t *testing.T, issues []ghx.Issue, err error) *[]issueListCall {
	t.Helper()
	calls := &[]issueListCall{}
	prev := errorReportIssueList
	errorReportIssueList = func(dir, repo string, labels []string, limit int) ([]ghx.Issue, error) {
		*calls = append(*calls, issueListCall{dir: dir, repo: repo, labels: append([]string(nil), labels...), limit: limit})
		return issues, err
	}
	t.Cleanup(func() { errorReportIssueList = prev })
	return calls
}

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
	redirectTempDir(t)
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

// TestErrorReportPrepare_ResultHasNoMirroredManifestFields pins
// errorReportPrepare's result to the manifest path and the duplicate-issue
// search result. prepare_orchestrator forwards those values (plus its own
// mode), and the orchestrator agent reads every other value from the manifest
// file, so mirrored fields would be dead. With no match and no failure,
// similarIssues is an empty array (not null) and similarIssuesNote is absent.
func TestErrorReportPrepare_ResultHasNoMirroredManifestFields(t *testing.T) {
	redirectTempDir(t)
	out, err := errorReportPrepare(t.TempDir(), ErrorReportPrepareIn{
		Skill:     "ship",
		Step:      "step-1",
		Operation: "do-thing",
		Error:     "boom",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	data, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	if len(m) != 2 || m["manifestPath"] == nil || m["manifestPath"] == "" {
		t.Errorf("result = %v, want only a non-empty manifestPath and similarIssues", m)
	}
	similar, ok := m["similarIssues"].([]any)
	if !ok || len(similar) != 0 {
		t.Errorf("similarIssues = %#v, want an empty array (not null)", m["similarIssues"])
	}
}

func TestErrorReportPrepare_OptionalFieldsDefaultToEmptyString(t *testing.T) {
	redirectTempDir(t)
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
	redirectTempDir(t)
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

// ---------------------------------------------------------------------------
// Duplicate-issue search
// ---------------------------------------------------------------------------

// TestErrorReportSimilarIssues_FiltersByTitlePrefixAndPassesGhArgs checks that
// the search asks gh for the labels tooling-error and the skill (the label
// filter runs in gh), once, against the target repo with limit 10, and that
// the Go title filter keeps only the issues whose title starts with
// "[<skill>]". A title with a longer tag, a missing bracket or the tag in the
// middle is dropped, and the kept issues keep gh's order.
func TestErrorReportSimilarIssues_FiltersByTitlePrefixAndPassesGhArgs(t *testing.T) {
	calls := stubErrorReportIssueList(t, []ghx.Issue{
		{Number: 91, Title: "[harden] pre-flight stop", URL: "https://example.test/91"},
		{Number: 40, Title: "[plan] unrelated", URL: "https://example.test/40"},
		{Number: 55, Title: "[hardening] other tag", URL: "https://example.test/55"},
		{Number: 56, Title: "harden without brackets", URL: "https://example.test/56"},
		{Number: 57, Title: "see [harden] in the middle", URL: "https://example.test/57"},
		{Number: 12, Title: "[harden] second match", URL: "https://example.test/12"},
	}, nil)

	got, note := errorReportSimilarIssues("/some/root", "harden")

	want := []ghx.Issue{
		{Number: 91, Title: "[harden] pre-flight stop", URL: "https://example.test/91"},
		{Number: 12, Title: "[harden] second match", URL: "https://example.test/12"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("similar issues = %+v, want %+v", got, want)
	}
	if note != "" {
		t.Errorf("note = %q, want empty when the search ran", note)
	}
	wantCalls := []issueListCall{{dir: "/some/root", repo: "rnagrodzki/sdlc-plugin", labels: []string{"tooling-error", "harden"}, limit: 10}}
	if !reflect.DeepEqual(*calls, wantCalls) {
		t.Errorf("gh issue list calls = %+v, want %+v", *calls, wantCalls)
	}
}

// TestErrorReportSimilarIssues_NoMatchIsEmptyAndNilFree checks the no-match
// case: gh returns nothing, or returns only issues of other skills. The result
// is a non-nil empty slice (it encodes as []) and the note is absent.
func TestErrorReportSimilarIssues_NoMatchIsEmptyAndNilFree(t *testing.T) {
	tests := []struct {
		name   string
		issues []ghx.Issue
	}{
		{name: "gh returns nil", issues: nil},
		{name: "gh returns only other skills", issues: []ghx.Issue{{Number: 3, Title: "[plan] x", URL: "u"}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			stubErrorReportIssueList(t, tc.issues, nil)

			got, note := errorReportSimilarIssues("/root", "harden")

			if got == nil || len(got) != 0 {
				t.Errorf("similar issues = %#v, want a non-nil empty slice", got)
			}
			if note != "" {
				t.Errorf("note = %q, want empty", note)
			}
		})
	}
}

// TestErrorReportSimilarIssues_FailureRows checks one search failure for each
// kind of failure: gh missing, gh exit with the first stderr line, gh exit
// with blank or padded stderr, output that is not JSON, and any other error.
// Each gives an empty (non-nil) issue list and the exact note text.
func TestErrorReportSimilarIssues_FailureRows(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantNote string
	}{
		{
			name:     "gh not installed",
			err:      errors.Join(ghx.ErrGHNotFound, errors.New("executable file not found")),
			wantNote: "duplicate search skipped: gh not found",
		},
		{
			name:     "gh exits non-zero, first line of stderr",
			err:      &ghx.ExitError{Command: "gh issue list", ExitCode: 1, Stderr: "HTTP 403: API rate limit exceeded\nTry again later"},
			wantNote: "duplicate search skipped: gh issue list failed: HTTP 403: API rate limit exceeded",
		},
		{
			name:     "gh exits non-zero, no stderr text",
			err:      &ghx.ExitError{Command: "gh issue list", ExitCode: 2},
			wantNote: "duplicate search skipped: gh issue list failed: exit code 2",
		},
		{
			name:     "gh exits non-zero, stderr of blank lines only",
			err:      &ghx.ExitError{Command: "gh issue list", ExitCode: 3, Stderr: "  \n "},
			wantNote: "duplicate search skipped: gh issue list failed: exit code 3",
		},
		{
			name:     "gh exits non-zero, padded stderr after blank lines",
			err:      &ghx.ExitError{Command: "gh issue list", ExitCode: 1, Stderr: "\n\n  HTTP 401: bad credentials \nsecond line"},
			wantNote: "duplicate search skipped: gh issue list failed: HTTP 401: bad credentials",
		},
		{
			name:     "gh output is not JSON",
			err:      errors.Join(ghx.ErrNotJSON, errors.New("invalid character 'h'")),
			wantNote: "duplicate search skipped: gh issue list output is not JSON",
		},
		{
			name:     "any other failure, first line of the error",
			err:      errors.New("ghx: IssueList: execx: gh: output exceeded cap\nsecond line"),
			wantNote: "duplicate search skipped: gh issue list failed: ghx: IssueList: execx: gh: output exceeded cap",
		},
		{
			name:     "any other failure, padded error text after blank lines",
			err:      errors.New("\n  ghx: IssueList: chdir /gone: no such file or directory \nsecond line"),
			wantNote: "duplicate search skipped: gh issue list failed: ghx: IssueList: chdir /gone: no such file or directory",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			stubErrorReportIssueList(t, []ghx.Issue{{Number: 1, Title: "[harden] ignored on failure", URL: "u"}}, tc.err)

			got, note := errorReportSimilarIssues("/root", "harden")

			if got == nil || len(got) != 0 {
				t.Errorf("similar issues = %#v, want a non-nil empty slice on failure", got)
			}
			if note != tc.wantNote {
				t.Errorf("note = %q, want %q", note, tc.wantNote)
			}
		})
	}
}

// TestErrorReportPrepare_ResultCarriesSimilarIssuesAndNote checks that
// errorReportPrepare puts the search result in its output and that a search
// failure does not fail the call: the manifest is still written, the issues
// are [] and the note says why.
func TestErrorReportPrepare_ResultCarriesSimilarIssuesAndNote(t *testing.T) {
	in := ErrorReportPrepareIn{Skill: "harden", Step: "step-1", Operation: "do-thing", Error: "boom"}

	t.Run("match", func(t *testing.T) {
		redirectTempDir(t)
		match := ghx.Issue{Number: 91, Title: "[harden] pre-flight stop", URL: "https://example.test/91"}
		calls := stubErrorReportIssueList(t, []ghx.Issue{match, {Number: 2, Title: "[plan] x", URL: "u"}}, nil)
		root := t.TempDir()

		out, err := errorReportPrepare(root, in)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !reflect.DeepEqual(out.SimilarIssues, []ghx.Issue{match}) {
			t.Errorf("SimilarIssues = %+v, want only the [harden] issue", out.SimilarIssues)
		}
		if out.SimilarIssuesNote != "" {
			t.Errorf("SimilarIssuesNote = %q, want empty", out.SimilarIssuesNote)
		}
		if len(*calls) != 1 || (*calls)[0].dir != root {
			t.Errorf("gh issue list calls = %+v, want one call in dir %q", *calls, root)
		}
	})

	t.Run("search failure", func(t *testing.T) {
		redirectTempDir(t)
		stubErrorReportIssueList(t, nil, &ghx.ExitError{Command: "gh issue list", ExitCode: 1, Stderr: "gh auth login required"})

		out, err := errorReportPrepare(t.TempDir(), in)
		if err != nil {
			t.Fatalf("a search failure must not fail the call, got: %v", err)
		}
		if out.SimilarIssues == nil || len(out.SimilarIssues) != 0 {
			t.Errorf("SimilarIssues = %#v, want a non-nil empty slice", out.SimilarIssues)
		}
		if want := "duplicate search skipped: gh issue list failed: gh auth login required"; out.SimilarIssuesNote != want {
			t.Errorf("SimilarIssuesNote = %q, want %q", out.SimilarIssuesNote, want)
		}
		if manifest := readErrorReportManifest(t, out.ManifestPath); manifest["skill"] != "harden" {
			t.Errorf("manifest skill = %v, want harden: the manifest must be written despite the failure", manifest["skill"])
		}
	})
}

// ---------------------------------------------------------------------------
// errorReportOutToPrepareOut
// ---------------------------------------------------------------------------

// TestErrorReportOutToPrepareOut checks the mapping from the error_report core
// result to the prepare_orchestrator output: path, mode and note are copied,
// similarIssues is always set (to [] for nil or empty input), and next holds
// the base text for no match, the offer sentence with the count for one or
// more matches, and the capitalized skip note for a skipped search.
func TestErrorReportOutToPrepareOut(t *testing.T) {
	one := ghx.Issue{Number: 91, Title: "[harden] pre-flight stop", URL: "https://example.test/91"}
	two := ghx.Issue{Number: 12, Title: "[harden] second", URL: "https://example.test/12"}
	tests := []struct {
		name       string
		in         ErrorReportPrepareOut
		wantIssues []ghx.Issue
		wantNote   string
		wantNext   string
	}{
		{
			name:       "nil issues",
			in:         ErrorReportPrepareOut{ManifestPath: "/m/manifest.json"},
			wantIssues: []ghx.Issue{},
			wantNext:   prepareNextErrorReport,
		},
		{
			name:       "empty issues",
			in:         ErrorReportPrepareOut{ManifestPath: "/m/manifest.json", SimilarIssues: []ghx.Issue{}},
			wantIssues: []ghx.Issue{},
			wantNext:   prepareNextErrorReport,
		},
		{
			name:       "skipped search keeps the note and ends next with it",
			in:         ErrorReportPrepareOut{ManifestPath: "/m/manifest.json", SimilarIssuesNote: "duplicate search skipped: gh not found"},
			wantIssues: []ghx.Issue{},
			wantNote:   "duplicate search skipped: gh not found",
			wantNext:   prepareNextErrorReport + " Duplicate search skipped: gh not found",
		},
		{
			name:       "one match",
			in:         ErrorReportPrepareOut{ManifestPath: "/m/manifest.json", SimilarIssues: []ghx.Issue{one}},
			wantIssues: []ghx.Issue{one},
			wantNext:   prepareNextErrorReport + " After Step 6, Step 6b offers the 1 similar open issue(s) before a new issue.",
		},
		{
			name:       "two matches",
			in:         ErrorReportPrepareOut{ManifestPath: "/m/manifest.json", SimilarIssues: []ghx.Issue{one, two}},
			wantIssues: []ghx.Issue{one, two},
			wantNext:   prepareNextErrorReport + " After Step 6, Step 6b offers the 2 similar open issue(s) before a new issue.",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := errorReportOutToPrepareOut(tc.in, "error_report")

			if got.ManifestPath != "/m/manifest.json" || got.Mode != "error_report" {
				t.Errorf("ManifestPath/Mode = %q/%q, want /m/manifest.json/error_report", got.ManifestPath, got.Mode)
			}
			if got.SimilarIssues == nil {
				t.Fatal("SimilarIssues = nil, want a pointer to a slice in error_report mode")
			}
			if !reflect.DeepEqual(*got.SimilarIssues, tc.wantIssues) {
				t.Errorf("SimilarIssues = %#v, want %#v", *got.SimilarIssues, tc.wantIssues)
			}
			if got.SimilarIssuesNote != tc.wantNote {
				t.Errorf("SimilarIssuesNote = %q, want %q", got.SimilarIssuesNote, tc.wantNote)
			}
			if got.Next != tc.wantNext {
				t.Errorf("Next = %q, want %q", got.Next, tc.wantNext)
			}
			if got.CustomInstructions != nil || got.PreflightFindingCount != 0 {
				t.Errorf("harden-only fields set in error_report output: %+v", got)
			}
		})
	}
}

// TestErrorReportNext_TellsSkippedSearchFromNoMatch runs the duplicate search
// with a fake gh and maps its result the way prepareOrchestrator does. A search
// that ran and found nothing leaves next as the base text. Each kind of search
// failure ends next with "Duplicate search skipped: <reason>", so a skipped
// search does not read as "no match".
func TestErrorReportNext_TellsSkippedSearchFromNoMatch(t *testing.T) {
	tests := []struct {
		name     string
		issues   []ghx.Issue
		err      error
		wantNext string
	}{
		{
			name:     "no match",
			wantNext: prepareNextErrorReport,
		},
		{
			name:     "only issues of other skills",
			issues:   []ghx.Issue{{Number: 3, Title: "[plan] x", URL: "u"}},
			wantNext: prepareNextErrorReport,
		},
		{
			name:     "gh not installed",
			err:      errors.Join(ghx.ErrGHNotFound, errors.New("executable file not found")),
			wantNext: prepareNextErrorReport + " Duplicate search skipped: gh not found",
		},
		{
			name:     "gh exits non-zero",
			err:      &ghx.ExitError{Command: "gh issue list", ExitCode: 1, Stderr: "HTTP 403: API rate limit exceeded\nTry again later"},
			wantNext: prepareNextErrorReport + " Duplicate search skipped: gh issue list failed: HTTP 403: API rate limit exceeded",
		},
		{
			name:     "gh output is not JSON",
			err:      errors.Join(ghx.ErrNotJSON, errors.New("invalid character 'h'")),
			wantNext: prepareNextErrorReport + " Duplicate search skipped: gh issue list output is not JSON",
		},
		{
			name:     "any other failure",
			err:      errors.New("ghx: IssueList: chdir /gone: no such file or directory"),
			wantNext: prepareNextErrorReport + " Duplicate search skipped: gh issue list failed: ghx: IssueList: chdir /gone: no such file or directory",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			stubErrorReportIssueList(t, tc.issues, tc.err)

			similar, note := errorReportSimilarIssues("/root", "harden")
			got := errorReportOutToPrepareOut(ErrorReportPrepareOut{ManifestPath: "/m", SimilarIssues: similar, SimilarIssuesNote: note}, "error_report")

			if got.Next != tc.wantNext {
				t.Errorf("Next = %q, want %q", got.Next, tc.wantNext)
			}
			if (tc.err == nil) != (got.SimilarIssuesNote == "") {
				t.Errorf("SimilarIssuesNote = %q, want it empty only when the search ran", got.SimilarIssuesNote)
			}
		})
	}
}

// TestPrepareOrchestratorOut_SimilarIssuesWireShape checks the JSON the MCP
// server sends: error_report mode always carries similarIssues (as [] when
// nothing matches) and omits similarIssuesNote unless the search was skipped;
// harden mode carries neither key.
func TestPrepareOrchestratorOut_SimilarIssuesWireShape(t *testing.T) {
	errorReport := orchestratorOutputJSON(t, errorReportOutToPrepareOut(ErrorReportPrepareOut{ManifestPath: "/m"}, "error_report"))
	issues, ok := errorReport["similarIssues"].([]any)
	if !ok || len(issues) != 0 {
		t.Errorf("error_report similarIssues = %#v, want an empty array", errorReport["similarIssues"])
	}
	if _, present := errorReport["similarIssuesNote"]; present {
		t.Errorf("error_report output has similarIssuesNote although the search ran: %v", errorReport)
	}

	harden := orchestratorOutputJSON(t, hardenOutToPrepareOut(HardenPrepareOut{ManifestPath: "/m"}, "harden"))
	for _, key := range []string{"similarIssues", "similarIssuesNote"} {
		if _, present := harden[key]; present {
			t.Errorf("harden output has %s: %v", key, harden)
		}
	}
}

// TestPrepareOrchestrator_ErrorReportReturnsSimilarIssues calls the
// prepare_orchestrator dispatcher in error_report mode with a fake search that
// finds two issues. Only the one whose title starts with the skill tag is
// returned, and next holds the offer sentence for 1 issue.
func TestPrepareOrchestrator_ErrorReportReturnsSimilarIssues(t *testing.T) {
	redirectTempDir(t)
	newOrchestratorRepo(t, "")
	match := ghx.Issue{Number: 91, Title: "[harden] pre-flight stop", URL: "https://example.test/91"}
	calls := stubErrorReportIssueList(t, []ghx.Issue{match, {Number: 40, Title: "[plan] unrelated", URL: "u"}}, nil)

	out, err := prepareOrchestrator(PrepareOrchestratorIn{
		Mode:      "error_report",
		Skill:     "harden",
		Step:      "step-1",
		Operation: "do-thing",
		Error:     "boom",
	})
	if err != nil {
		t.Fatalf("prepareOrchestrator: %v", err)
	}

	wire := orchestratorOutputJSON(t, out)
	wantIssues := []any{map[string]any{"number": float64(91), "title": "[harden] pre-flight stop", "url": "https://example.test/91"}}
	if !reflect.DeepEqual(wire["similarIssues"], wantIssues) {
		t.Errorf("wire similarIssues = %#v, want %#v", wire["similarIssues"], wantIssues)
	}
	wantNext := prepareNextErrorReport + " After Step 6, Step 6b offers the 1 similar open issue(s) before a new issue."
	if wire["next"] != wantNext {
		t.Errorf("wire next = %v, want %q", wire["next"], wantNext)
	}
	if len(*calls) != 1 || !reflect.DeepEqual((*calls)[0].labels, []string{"tooling-error", "harden"}) {
		t.Errorf("gh issue list calls = %+v, want one call with labels tooling-error and harden", *calls)
	}
}
