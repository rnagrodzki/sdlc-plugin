package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/hardensurfaces"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
	"github.com/rnagrodzki/sdlc-plugin/internal/state"
)

// ---------------------------------------------------------------------------
// KD5 config-version gate
// ---------------------------------------------------------------------------

func TestHardenPrepare_KD5Gate(t *testing.T) {
	root := t.TempDir()
	sdlcDir := filepath.Join(root, paths.DataDir)
	if err := os.MkdirAll(sdlcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// A stale schemaVersion (below configmigrate.CurrentSchemaVersion) is a
	// genuine migration-needed condition (a merely-absent config.json is not).
	if err := os.WriteFile(filepath.Join(sdlcDir, "config.json"), []byte(`{"schemaVersion": 1}`), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := hardenPrepare(root, root, HardenPrepareIn{
		FailureText:     "boom",
		Skill:           "ship",
		SkipConfigCheck: false,
	})
	if err == nil {
		t.Fatal("expected an error from the KD5 config-version gate, got nil")
	}
	var dataErr *mcpserver.DataError
	if !errorsAsDataError(err, &dataErr) {
		t.Fatalf("expected *mcpserver.DataError, got %T: %v", err, err)
	}
}

func TestHardenPrepare_SkipConfigCheckBypassesGate(t *testing.T) {
	// SkipConfigCheck only disables hardenPrepare's own explicit KD5 gate
	// (configmigrate.Verify). It does not make a legacy .sdlc-v2/config.json
	// invisible: internal/config's legacyMarkers includes that path, so
	// guardrailsPreflight's config.ReadSection call still catches it and
	// fails pre-flight with a non-DataError, non-"config-version:" message.
	root := t.TempDir()
	sdlcDir := filepath.Join(root, paths.DataDir)
	if err := os.MkdirAll(sdlcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sdlcDir, "config.json"), []byte(`{"schemaVersion": 1}`), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := hardenPrepare(root, root, HardenPrepareIn{
		FailureText:     "boom",
		Skill:           "ship",
		SkipConfigCheck: true,
	})
	// A schemaVersion-stamped config.json also unconditionally trips
	// config.ReadSection's own permanent pre-v5 rejection (independent of
	// the KD5 gate), so guardrail pre-flight still fails downstream — this
	// fixture cannot reach a full manifest either way. What SkipConfigCheck
	// controls is specifically whether hardenPrepare's own explicit
	// configmigrate.Verify call (the KD5 gate) contributes a *DataError
	// prefixed "config-version:"; assert that specific error is bypassed.
	if err == nil {
		t.Fatal("expected pre-flight to still fail on the legacy config, got nil")
	}
	if _, isDataErr := err.(*mcpserver.DataError); isDataErr {
		t.Fatalf("expected the KD5 config-version DataError to be bypassed, got one anyway: %v", err)
	}
	if containsSubstr(err.Error(), "config-version:") {
		t.Fatalf("expected no config-version gate message with SkipConfigCheck=true, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// R19 --from-issue / --failure-text
// ---------------------------------------------------------------------------

func TestHardenPrepare_MutualExclusion(t *testing.T) {
	root := t.TempDir()
	_, err := hardenPrepare(root, root, HardenPrepareIn{
		FailureText:     "boom",
		FromIssue:       "42",
		Skill:           "ship",
		SkipConfigCheck: true,
	})
	if err == nil {
		t.Fatal("expected mutual-exclusion error, got nil")
	}
	var domainErr *mcpserver.DomainError
	if !errorsAsDomainError(err, &domainErr) {
		t.Fatalf("expected *mcpserver.DomainError, got %T: %v", err, err)
	}
}

func TestHardenPrepare_FromIssueInvalidNumber(t *testing.T) {
	root := t.TempDir()
	_, err := hardenPrepare(root, root, HardenPrepareIn{
		FromIssue:       "not-a-number",
		Skill:           "ship",
		SkipConfigCheck: true,
	})
	if err == nil {
		t.Fatal("expected invalid-issue-number error, got nil")
	}
	var domainErr *mcpserver.DomainError
	if !errorsAsDomainError(err, &domainErr) {
		t.Fatalf("expected *mcpserver.DomainError, got %T: %v", err, err)
	}
}

// TestHardenPrepare_FromIssueZeroRejected pins that fromIssue must be a
// positive integer, as its error message says: 0 is refused before any
// gh call.
func TestHardenPrepare_FromIssueZeroRejected(t *testing.T) {
	for _, n := range []string{"0", "00", " 0 "} {
		root := t.TempDir()
		_, err := hardenPrepare(root, root, HardenPrepareIn{
			FromIssue:       n,
			Skill:           "ship",
			SkipConfigCheck: true,
		})
		var domainErr *mcpserver.DomainError
		if !errorsAsDomainError(err, &domainErr) {
			t.Fatalf("fromIssue %q: expected *mcpserver.DomainError, got %T: %v", n, err, err)
		}
		if !containsSubstr(domainErr.Msg, "must be a positive integer") {
			t.Errorf("fromIssue %q: message = %q, want it to say positive integer", n, domainErr.Msg)
		}
	}
}

// ---------------------------------------------------------------------------
// Required fields
// ---------------------------------------------------------------------------

func TestHardenPrepare_MissingRequiredFields(t *testing.T) {
	root := t.TempDir()
	_, err := hardenPrepare(root, root, HardenPrepareIn{SkipConfigCheck: true})
	if err == nil {
		t.Fatal("expected a missing-required-field error, got nil")
	}
	var domainErr *mcpserver.DomainError
	if !errorsAsDomainError(err, &domainErr) {
		t.Fatalf("expected *mcpserver.DomainError, got %T: %v", err, err)
	}
	if !containsSubstr(domainErr.Msg, "failureText") || !containsSubstr(domainErr.Msg, "skill") {
		t.Fatalf("expected message to mention both missing fields, got: %s", domainErr.Msg)
	}
	// The suggestion must name the live tool, not the retired harden_prepare.
	if !containsSubstr(domainErr.Suggestion, "prepare_orchestrator") || containsSubstr(domainErr.Suggestion, "harden_prepare") {
		t.Errorf("suggestion should point at prepare_orchestrator, got: %s", domainErr.Suggestion)
	}
}

// ---------------------------------------------------------------------------
// R16 pre-flight
// ---------------------------------------------------------------------------

// TestHardenPrepare_PreflightGuardrailFindingInManifest pins that an invalid
// guardrail id does not stop the run: hardenPrepare returns ok, writes the
// manifest, and lists the finding with its surface, id, file, severity and
// fix. The description is short, so bytesOver is absent.
func TestHardenPrepare_PreflightGuardrailFindingInManifest(t *testing.T) {
	redirectTempDir(t)
	root := t.TempDir()
	writeFile(t, filepath.Join(root, paths.DataDir, "config.toml"), ""+
		"[plan.guardrails.Bad_ID]\n"+
		"description = \"desc\"\n")

	out, err := hardenPrepare(root, root, HardenPrepareIn{
		FailureText:     "boom",
		Skill:           "ship",
		SkipConfigCheck: true,
	})
	if err != nil {
		t.Fatalf("pre-flight finding must not abort: %v", err)
	}
	if out.PreflightFindingCount != 1 || out.ManifestPath == "" {
		t.Fatalf("want 1 finding and a manifest, got %d, %q", out.PreflightFindingCount, out.ManifestPath)
	}

	findings := manifestPreflightFindings(t, out.ManifestPath)
	if len(findings) != 1 {
		t.Fatalf("want 1 manifest finding, got %+v", findings)
	}
	f := findings[0]
	if f["surface"] != "plan-guardrails" || f["id"] != "Bad_ID" || f["severity"] != "error" ||
		f["file"] != ".sdlc-v2/config.toml" || f["fix"] != guardrailIDFormatFix {
		t.Errorf("unexpected finding: %+v", f)
	}
	if msg, _ := f["message"].(string); !containsSubstr(msg, "kebab-case") {
		t.Errorf("message = %q, want the kebab-case validator message", msg)
	}
	if _, present := f["bytesOver"]; present {
		t.Errorf("bytesOver must be omitted for a short description, got %+v", f["bytesOver"])
	}
}

// TestHardenPrepare_PreflightUsesContentRoot pins that the guardrail
// pre-flight (guardrailsPreflight) reads from contentRoot (the active
// worktree), not root (the main worktree). An invalid guardrail that exists
// only in root gives no finding; the same invalid guardrail placed in
// contentRoot gives one finding, and neither case stops the run.
func TestHardenPrepare_PreflightUsesContentRoot(t *testing.T) {
	redirectTempDir(t)
	badConfig := "" +
		"[plan.guardrails.Bad_ID]\n" +
		"description = \"desc\"\n"

	t.Run("invalid guardrail only in root is ignored", func(t *testing.T) {
		root := t.TempDir()
		contentRoot := t.TempDir()
		writeFile(t, filepath.Join(root, paths.DataDir, "config.toml"), badConfig)

		out, err := hardenPrepare(root, contentRoot, HardenPrepareIn{
			FailureText:     "boom",
			Skill:           "ship",
			SkipConfigCheck: true,
		})
		if err != nil {
			t.Fatalf("expected no pre-flight error for a root-only invalid guardrail, got: %v", err)
		}
		if out.PreflightFindingCount != 0 {
			t.Fatalf("want 0 findings for a root-only invalid guardrail, got %d", out.PreflightFindingCount)
		}
	})

	t.Run("same invalid guardrail in contentRoot", func(t *testing.T) {
		root := t.TempDir()
		contentRoot := t.TempDir()
		writeFile(t, filepath.Join(contentRoot, paths.DataDir, "config.toml"), badConfig)

		out, err := hardenPrepare(root, contentRoot, HardenPrepareIn{
			FailureText:     "boom",
			Skill:           "ship",
			SkipConfigCheck: true,
		})
		if err != nil {
			t.Fatalf("pre-flight finding must not abort: %v", err)
		}
		if out.PreflightFindingCount != 1 {
			t.Fatalf("want 1 finding from contentRoot, got %d", out.PreflightFindingCount)
		}
	})
}

// TestHardenPrepare_PreflightDimensionFindingInManifest pins that a
// review-dimension file with no frontmatter does not stop the run: it is one
// finding with severity error, the file name as id and an empty fix.
func TestHardenPrepare_PreflightDimensionFindingInManifest(t *testing.T) {
	redirectTempDir(t)
	root := t.TempDir()
	writeFile(t, filepath.Join(root, paths.DataDir, "review-dimensions", "bad.md"), "no frontmatter here\n")

	out, err := hardenPrepare(root, root, HardenPrepareIn{
		FailureText:     "boom",
		Skill:           "ship",
		SkipConfigCheck: true,
	})
	if err != nil || out.PreflightFindingCount != 1 {
		t.Fatalf("want 1 dimension finding and no error, got %d, %v", out.PreflightFindingCount, err)
	}

	findings := manifestPreflightFindings(t, out.ManifestPath)
	if len(findings) != 1 {
		t.Fatalf("want 1 manifest finding, got %+v", findings)
	}
	f := findings[0]
	if f["surface"] != "review-dimensions" || f["id"] != "bad" || f["severity"] != "error" ||
		f["file"] != ".sdlc-v2/review-dimensions/bad.md" || f["fix"] != "" ||
		f["message"] != "Missing YAML frontmatter block (--- delimiters)" {
		t.Errorf("unexpected finding: %+v", f)
	}
}

// TestHardenPrepare_PreflightDimensionWarningInManifest pins that a dimension
// warning goes into the same list as the errors, with severity warning and the
// frontmatter name as id.
func TestHardenPrepare_PreflightDimensionWarningInManifest(t *testing.T) {
	redirectTempDir(t)
	root := t.TempDir()
	writeFile(t, filepath.Join(root, paths.DataDir, "review-dimensions", "sec.md"),
		"---\n"+
			"name: security-review\n"+
			"severity: bogus\n"+
			"description: Check for security issues in the diff.\n"+
			"triggers:\n"+
			"  - \"**/*.go\"\n"+
			"---\n"+
			"# Security\n\nLook for injection and secrets in the diff.\n")
	writeFile(t, filepath.Join(root, paths.DataDir, "review-dimensions", "bad.md"), "no frontmatter here\n")

	out, err := hardenPrepare(root, root, HardenPrepareIn{
		FailureText:     "boom",
		Skill:           "ship",
		SkipConfigCheck: true,
	})
	if err != nil {
		t.Fatalf("pre-flight finding must not abort: %v", err)
	}
	if out.PreflightFindingCount != 2 {
		t.Fatalf("want 2 findings (1 error, 1 warning), got %d", out.PreflightFindingCount)
	}

	bySeverity := map[string]map[string]any{}
	for _, f := range manifestPreflightFindings(t, out.ManifestPath) {
		bySeverity[f["severity"].(string)] = f
	}
	w, ok := bySeverity["warning"]
	if !ok {
		t.Fatalf("want one warning finding, got %+v", bySeverity)
	}
	if w["id"] != "security-review" || w["surface"] != "review-dimensions" || w["file"] != ".sdlc-v2/review-dimensions/sec.md" {
		t.Errorf("unexpected warning finding: %+v", w)
	}
	if e, ok := bySeverity["error"]; !ok || e["id"] != "bad" {
		t.Errorf("want one error finding with id bad, got %+v", bySeverity["error"])
	}
}

// TestHardenPrepare_PreflightLongDescriptionBytesOver pins that a
// 1474-byte guardrail description gives ok, a written manifest, and one
// finding with bytesOver 450 and the split fix text.
func TestHardenPrepare_PreflightLongDescriptionBytesOver(t *testing.T) {
	redirectTempDir(t)
	root := t.TempDir()
	writeFile(t, filepath.Join(root, paths.DataDir, "config.toml"), ""+
		"[execute.guardrails.long-rule]\n"+
		"description = \""+strings.Repeat("a", 1474)+"\"\n")

	out, err := hardenPrepare(root, root, HardenPrepareIn{
		FailureText:     "boom",
		Skill:           "ship",
		SkipConfigCheck: true,
	})
	if err != nil {
		t.Fatalf("pre-flight finding must not abort: %v", err)
	}
	if out.PreflightFindingCount != 1 || out.ManifestPath == "" {
		t.Fatalf("want 1 finding and a manifest, got %d, %q", out.PreflightFindingCount, out.ManifestPath)
	}

	findings := manifestPreflightFindings(t, out.ManifestPath)
	if len(findings) != 1 {
		t.Fatalf("want 1 manifest finding, got %+v", findings)
	}
	f := findings[0]
	if f["surface"] != "execute-guardrails" || f["id"] != "long-rule" || f["fix"] != guardrailDescLengthFix("long-rule") {
		t.Errorf("unexpected finding: %+v", f)
	}
	if f["bytesOver"] != float64(450) {
		t.Errorf("bytesOver = %v, want 450", f["bytesOver"])
	}
}

// TestHardenPrepare_PreflightCleanConfigEmptyList pins that clean rules give
// preflightFindings as an empty JSON array (never null) and a count of 0.
func TestHardenPrepare_PreflightCleanConfigEmptyList(t *testing.T) {
	redirectTempDir(t)
	root := t.TempDir()
	writeFile(t, filepath.Join(root, paths.DataDir, "config.toml"), ""+
		"[plan.guardrails.good-rule]\n"+
		"description = \"Every task names its files.\"\n")

	out, err := hardenPrepare(root, root, HardenPrepareIn{
		FailureText:     "boom",
		Skill:           "ship",
		SkipConfigCheck: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.PreflightFindingCount != 0 {
		t.Fatalf("want 0 findings, got %d", out.PreflightFindingCount)
	}
	raw, present := readHardenManifest(t, out.ManifestPath)["preflightFindings"]
	if !present {
		t.Fatal("manifest has no preflightFindings key")
	}
	list, ok := raw.([]any)
	if !ok || len(list) != 0 {
		t.Fatalf("preflightFindings = %#v, want an empty array (not null)", raw)
	}
}

// TestHardenPrepare_PreflightInvalidTOMLStops pins that a config that does not
// parse stops the run and writes no manifest: hardenPrepare returns the
// InfraError of validateGuardrailsAction and creates no manifest directory.
func TestHardenPrepare_PreflightInvalidTOMLStops(t *testing.T) {
	tmpDir := redirectTempDir(t)
	root := t.TempDir()
	writeFile(t, filepath.Join(root, paths.DataDir, "config.toml"), "[plan\nthis is = = not toml\n")

	out, err := hardenPrepare(root, root, HardenPrepareIn{
		FailureText:     "boom",
		Skill:           "ship",
		SkipConfigCheck: true,
	})
	var infraErr *mcpserver.InfraError
	if !errorsAsInfraError(err, &infraErr) {
		t.Fatalf("expected *mcpserver.InfraError, got %T: %v", err, err)
	}
	if !containsSubstr(infraErr.Suggestion, "is valid TOML and readable, then retry.") {
		t.Errorf("unexpected suggestion: %s", infraErr.Suggestion)
	}
	if infraErr.Cause == nil {
		t.Error("InfraError must carry the wrapped Cause")
	}
	if out.ManifestPath != "" {
		t.Fatalf("expected no manifest path when the config does not parse, got %q", out.ManifestPath)
	}
	assertNoHardenManifestDir(t, tmpDir)
}

// TestHardenPrepare_PreflightUnreadableDimensionsDirStops pins that an
// unreadable review-dimensions directory stops the run: hardenPrepare returns
// an InfraError that names the recovery and creates no manifest directory.
func TestHardenPrepare_PreflightUnreadableDimensionsDirStops(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root reads a 0o000 directory, so the permission error cannot happen")
	}
	tmpDir := redirectTempDir(t)
	root := t.TempDir()
	dir := filepath.Join(root, paths.DataDir, "review-dimensions")
	writeFile(t, filepath.Join(dir, "sec.md"), "no frontmatter here\n")
	if err := os.Chmod(dir, 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(dir, 0o755); err != nil {
			t.Errorf("restore dir mode: %v", err)
		}
	})

	out, err := hardenPrepare(root, root, HardenPrepareIn{
		FailureText:     "boom",
		Skill:           "ship",
		SkipConfigCheck: true,
	})
	var infraErr *mcpserver.InfraError
	if !errorsAsInfraError(err, &infraErr) {
		t.Fatalf("expected *mcpserver.InfraError, got %T: %v", err, err)
	}
	if infraErr.Suggestion != "Check read permission on .sdlc-v2/review-dimensions/, then retry prepare_orchestrator with mode harden." {
		t.Errorf("unexpected suggestion: %s", infraErr.Suggestion)
	}
	if infraErr.Cause == nil {
		t.Error("InfraError must carry the wrapped Cause")
	}
	if out.ManifestPath != "" {
		t.Fatalf("expected no manifest path when the directory cannot be read, got %q", out.ManifestPath)
	}
	assertNoHardenManifestDir(t, tmpDir)
}

// TestHardenPrepare_PreflightOneGuardrailManyFindingsShareBytesOver pins that
// every finding of one guardrail carries the bytesOver of that guardrail's
// description. The fixture has an id that breaks the kebab-case rule and a
// 1474-byte description, so it yields two findings with the same bytesOver of
// 450.
func TestHardenPrepare_PreflightOneGuardrailManyFindingsShareBytesOver(t *testing.T) {
	redirectTempDir(t)
	root := t.TempDir()
	writeFile(t, filepath.Join(root, paths.DataDir, "config.toml"), ""+
		"[plan.guardrails.Bad_ID]\n"+
		"description = \""+strings.Repeat("a", 1474)+"\"\n")

	out, err := hardenPrepare(root, root, HardenPrepareIn{
		FailureText:     "boom",
		Skill:           "ship",
		SkipConfigCheck: true,
	})
	if err != nil {
		t.Fatalf("pre-flight finding must not abort: %v", err)
	}
	if out.PreflightFindingCount != 2 {
		t.Fatalf("want 2 findings (id format, description length), got %d", out.PreflightFindingCount)
	}

	fixes := map[string]bool{}
	for _, f := range manifestPreflightFindings(t, out.ManifestPath) {
		if f["id"] != "Bad_ID" || f["bytesOver"] != float64(450) {
			t.Errorf("finding = %+v, want id Bad_ID and bytesOver 450", f)
		}
		fixes[f["fix"].(string)] = true
	}
	if !fixes[guardrailIDFormatFix] || !fixes[guardrailDescLengthFix("Bad_ID")] {
		t.Errorf("want one finding for the id format and one for the length, got fixes %v", fixes)
	}
}

// TestHardenOutToPrepareOut pins that the prepare_orchestrator output copies
// the harden fields a skill reads, and that next starts with
// prepareNextHarden and adds the findings sentence only when the count is
// above 0.
func TestHardenOutToPrepareOut(t *testing.T) {
	ci := map[string][]string{"plan-guardrails": {"keep ids short"}}

	t.Run("no findings", func(t *testing.T) {
		got := hardenOutToPrepareOut(HardenPrepareOut{ManifestPath: "/m.json", CustomInstructions: ci}, "harden")
		want := PrepareOrchestratorOut{ManifestPath: "/m.json", Mode: "harden", CustomInstructions: ci, PreflightFindingCount: 0, Next: prepareNextHarden}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %+v, want %+v", got, want)
		}
	})

	t.Run("two findings", func(t *testing.T) {
		got := hardenOutToPrepareOut(HardenPrepareOut{ManifestPath: "/m.json", CustomInstructions: ci, PreflightFindingCount: 2}, "harden")
		wantNext := prepareNextHarden + " 2 pre-flight findings: the harden orchestrator drafts repair proposals for them first."
		if got.Next != wantNext {
			t.Errorf("Next = %q, want %q", got.Next, wantNext)
		}
		if got.PreflightFindingCount != 2 || got.ManifestPath != "/m.json" || got.Mode != "harden" || !reflect.DeepEqual(got.CustomInstructions, ci) {
			t.Errorf("unexpected output: %+v", got)
		}
	})
}

// ---------------------------------------------------------------------------
// Manifest field fidelity
// ---------------------------------------------------------------------------

func TestHardenPrepare_ManifestFieldFidelity(t *testing.T) {
	root := t.TempDir()

	out, err := hardenPrepare(root, root, HardenPrepareIn{
		FailureText:     "  boom happened  ",
		Skill:           "  ship  ",
		Step:            "  step-1  ",
		Operation:       "  do-thing  ",
		ExitCode:        " ", // raw non-empty (a single space) — must NOT collapse to null
		ErrorType:       "  timeout  ",
		UserIntent:      "  fix it please  ",
		ArgsString:      "  --flag value  ",
		SkipConfigCheck: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	manifest := readHardenManifest(t, out.ManifestPath)

	failure := manifest["failure"].(map[string]any)
	if failure["text"] != "  boom happened  " {
		t.Errorf("failure.text = %q, want raw (untrimmed) value", failure["text"])
	}
	if failure["skill"] != "ship" {
		t.Errorf("failure.skill = %q, want trimmed", failure["skill"])
	}
	if failure["step"] != "step-1" {
		t.Errorf("failure.step = %q, want trimmed", failure["step"])
	}
	if failure["operation"] != "do-thing" {
		t.Errorf("failure.operation = %q, want trimmed", failure["operation"])
	}
	if failure["exitCode"] != " " {
		t.Errorf("failure.exitCode = %v, want raw single-space string (not null, not trimmed)", failure["exitCode"])
	}
	if failure["errorType"] != "timeout" {
		t.Errorf("failure.errorType = %q, want trimmed", failure["errorType"])
	}
	if failure["userIntent"] != "  fix it please  " {
		t.Errorf("failure.userIntent = %q, want raw (untrimmed) value", failure["userIntent"])
	}
	if failure["argsString"] != "  --flag value  " {
		t.Errorf("failure.argsString = %q, want raw (untrimmed) value", failure["argsString"])
	}

	if manifest["classification_hint"] != nil {
		t.Errorf("classification_hint = %v, want nil (no --from-issue)", manifest["classification_hint"])
	}
	if manifest["pluginRepoUrl"] != hardenPluginRepoURL {
		t.Errorf("pluginRepoUrl = %v, want %q", manifest["pluginRepoUrl"], hardenPluginRepoURL)
	}

	errs, ok := manifest["errors"].([]any)
	if !ok {
		t.Fatalf("errors field is not an array: %T", manifest["errors"])
	}
	// This test's temp root has no plugin installation, so
	// resolveErrorReportSkill legitimately soft-fails and contributes one
	// "error-report-skill" entry regardless of the fixture under test (see
	// TestResolveErrorReportSkill_SoftFailsWhenAbsent) — filter it out
	// before asserting on the surfaces this test actually exercises.
	if remaining := filterOutSurface(errs, "error-report-skill"); len(remaining) != 0 {
		t.Errorf("expected no non-error-report-skill errors, got %+v", remaining)
	}

	surfaces := manifest["surfaces"].(map[string]any)
	for _, key := range []string{"planGuardrails", "executeGuardrails", "reviewDimensions", "copilotInstructions"} {
		list, ok := surfaces[key].([]any)
		if !ok {
			t.Fatalf("surfaces.%s is not an array: %T", key, surfaces[key])
		}
		if len(list) != 0 {
			t.Errorf("surfaces.%s expected empty, got %+v", key, list)
		}
	}

	// With no config.toml the manifest still carries customInstructions: one
	// empty (non-null) list for each proposal surface id.
	ids := hardensurfaces.ProposalIDs()
	instructions, ok := manifest["customInstructions"].(map[string]any)
	if !ok {
		t.Fatalf("customInstructions is not an object: %T", manifest["customInstructions"])
	}
	if len(instructions) != len(ids) {
		t.Errorf("customInstructions has %d keys, want %d: %v", len(instructions), len(ids), instructions)
	}
	for _, id := range ids {
		list, ok := instructions[id].([]any)
		if !ok {
			t.Errorf("customInstructions.%s is not an array: %T", id, instructions[id])
			continue
		}
		if len(list) != 0 {
			t.Errorf("customInstructions.%s expected empty, got %+v", id, list)
		}
		if outList, ok := out.CustomInstructions[id]; !ok || outList == nil || len(outList) != 0 {
			t.Errorf("out.CustomInstructions[%s] = %#v, want a non-nil empty list", id, outList)
		}
	}
}

// hardenInstructionsConfig is a config.toml body with a list for two of the
// four proposal surfaces; the other two surfaces have no list.
const hardenInstructionsConfig = "" +
	"[harden.instructions]\n" +
	"plan-guardrails = [\"  Prefer a warning over an error.  \", \"\", \"Name the failing task.\"]\n" +
	"copilot-instructions = [\"Keep each rule to one line.\"]\n"

// TestHardenPrepare_ManifestCarriesConfiguredInstructions asserts that the
// [harden.instructions] lists of contentRoot reach both the manifest file and
// the returned output as the same map, trimmed and without blank items, and
// that the main root config.toml is not read for them.
func TestHardenPrepare_ManifestCarriesConfiguredInstructions(t *testing.T) {
	root := t.TempDir()
	contentRoot := t.TempDir()
	writeFile(t, filepath.Join(contentRoot, paths.DataDir, "config.toml"), hardenInstructionsConfig)
	writeFile(t, filepath.Join(root, paths.DataDir, "config.toml"), ""+
		"[harden.instructions]\n"+
		"review-dimensions = [\"Instruction from the main root.\"]\n")

	out, err := hardenPrepare(root, contentRoot, HardenPrepareIn{
		FailureText:     "boom",
		Skill:           "ship",
		SkipConfigCheck: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := map[string][]string{
		"plan-guardrails":      {"Prefer a warning over an error.", "Name the failing task."},
		"execute-guardrails":   {},
		"review-dimensions":    {},
		"copilot-instructions": {"Keep each rule to one line."},
	}
	if !reflect.DeepEqual(out.CustomInstructions, want) {
		t.Errorf("out.CustomInstructions = %#v, want %#v", out.CustomInstructions, want)
	}

	manifest := readHardenManifest(t, out.ManifestPath)
	raw, err := json.Marshal(manifest["customInstructions"])
	if err != nil {
		t.Fatalf("marshal manifest customInstructions: %v", err)
	}
	var fromManifest map[string][]string
	if err := json.Unmarshal(raw, &fromManifest); err != nil {
		t.Fatalf("unmarshal manifest customInstructions: %v", err)
	}
	if !reflect.DeepEqual(fromManifest, want) {
		t.Errorf("manifest customInstructions = %#v, want %#v", fromManifest, want)
	}
}

// TestHardenPrepare_InvalidInstructionsStopPrepare asserts that a loader error
// stops hardenPrepare before the manifest is written and comes back unchanged:
// the same DomainError that loadHardenInstructions returns, not a wrapped one.
func TestHardenPrepare_InvalidInstructionsStopPrepare(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, paths.DataDir, "config.toml"), ""+
		"[harden.instructions]\n"+
		"plan-guardrails = \"not a list\"\n")

	_, wantErr := loadHardenInstructions(root)
	if wantErr == nil {
		t.Fatal("fixture error: the loader accepted the invalid list")
	}

	out, err := hardenPrepare(root, root, HardenPrepareIn{
		FailureText:     "boom",
		Skill:           "ship",
		SkipConfigCheck: true,
	})
	if err == nil {
		t.Fatal("expected the loader error, got nil")
	}
	var domainErr *mcpserver.DomainError
	if !errorsAsDomainError(err, &domainErr) {
		t.Fatalf("expected an unwrapped *mcpserver.DomainError, got %T: %v", err, err)
	}
	if err.Error() != wantErr.Error() {
		t.Errorf("error = %q, want the loader error %q", err.Error(), wantErr.Error())
	}
	if !containsSubstr(domainErr.Msg, "harden.instructions.plan-guardrails") {
		t.Errorf("expected the message to name the key, got: %s", domainErr.Msg)
	}
	if domainErr.Suggestion == "" {
		t.Error("Suggestion is empty")
	}
	if out.ManifestPath != "" {
		t.Errorf("expected no manifest path when the loader fails, got %q", out.ManifestPath)
	}
}

func TestHardenPrepare_ExitCodeEmptyIsNull(t *testing.T) {
	root := t.TempDir()
	out, err := hardenPrepare(root, root, HardenPrepareIn{
		FailureText:     "boom",
		Skill:           "ship",
		SkipConfigCheck: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	manifest := readHardenManifest(t, out.ManifestPath)
	failure := manifest["failure"].(map[string]any)
	if failure["exitCode"] != nil {
		t.Errorf("exitCode = %v, want nil when not supplied", failure["exitCode"])
	}
}

// ---------------------------------------------------------------------------
// Surfaces: guardrails / review dimensions / copilot instructions
// ---------------------------------------------------------------------------

func TestHardenPrepare_LoadsGuardrailSurfaces(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, paths.DataDir, "config.toml"), ""+
		"[plan.guardrails.no-console-log]\n"+
		"severity = \"warning\"\n"+
		"description = \"Avoid console.log in production code.\"\n"+
		"\n"+
		"[execute.guardrails.no-severity-guardrail]\n"+
		"description = \"Missing severity should default to error.\"\n")

	out, err := hardenPrepare(root, root, HardenPrepareIn{
		FailureText:     "boom",
		Skill:           "ship",
		SkipConfigCheck: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	manifest := readHardenManifest(t, out.ManifestPath)
	surfaces := manifest["surfaces"].(map[string]any)

	plan := surfaces["planGuardrails"].([]any)
	if len(plan) != 1 {
		t.Fatalf("expected 1 plan guardrail, got %+v", plan)
	}
	g := plan[0].(map[string]any)
	if g["id"] != "no-console-log" || g["severity"] != "warning" || g["description"] != "Avoid console.log in production code." {
		t.Errorf("unexpected plan guardrail: %+v", g)
	}

	exec := surfaces["executeGuardrails"].([]any)
	if len(exec) != 1 {
		t.Fatalf("expected 1 execute guardrail, got %+v", exec)
	}
	eg := exec[0].(map[string]any)
	if eg["id"] != "no-severity-guardrail" || eg["severity"] != "error" {
		t.Errorf("expected id passed through and severity defaulted to 'error', got %+v", eg)
	}
}

// TestHardenPrepare_GuardrailsFromContentRoot pins Task 1's contract: both
// guardrail surfaces (loadSurfaceGuardrails for "plan" and "execute") load
// from contentRoot (the active worktree), not root (the main worktree). A
// guardrail defined only in root must not appear in the manifest; a
// guardrail defined only in contentRoot must.
func TestHardenPrepare_GuardrailsFromContentRoot(t *testing.T) {
	root := t.TempDir()
	contentRoot := t.TempDir()
	writeFile(t, filepath.Join(root, paths.DataDir, "config.toml"), ""+
		"[plan.guardrails.root-only-guardrail]\n"+
		"description = \"Should not appear — root is not the guardrail source.\"\n")
	writeFile(t, filepath.Join(contentRoot, paths.DataDir, "config.toml"), ""+
		"[plan.guardrails.content-root-guardrail]\n"+
		"severity = \"warning\"\n"+
		"description = \"Should appear — contentRoot is the guardrail source.\"\n")

	out, err := hardenPrepare(root, contentRoot, HardenPrepareIn{
		FailureText:     "boom",
		Skill:           "ship",
		SkipConfigCheck: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	manifest := readHardenManifest(t, out.ManifestPath)
	surfaces := manifest["surfaces"].(map[string]any)

	plan := surfaces["planGuardrails"].([]any)
	if len(plan) != 1 {
		t.Fatalf("expected 1 plan guardrail (from contentRoot only), got %+v", plan)
	}
	g := plan[0].(map[string]any)
	if g["id"] != "content-root-guardrail" {
		t.Errorf("expected the contentRoot guardrail, got %+v", g)
	}
}

func TestHardenPrepare_LoadsReviewDimensionsMetadataOnly(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, paths.DataDir, "review-dimensions", "security.md"),
		"---\n"+
			"name: security-review\n"+
			"severity: high\n"+
			"description: Check for security issues in the diff.\n"+
			"triggers:\n"+
			"  - \"**/*.go\"\n"+
			"model: opus\n"+
			"---\n"+
			"# Full body content\n\nThis should NOT appear in the manifest (metadata-only, RULING 1).\n")

	out, err := hardenPrepare(root, root, HardenPrepareIn{
		FailureText:     "boom",
		Skill:           "ship",
		SkipConfigCheck: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	manifest := readHardenManifest(t, out.ManifestPath)
	surfaces := manifest["surfaces"].(map[string]any)
	dims := surfaces["reviewDimensions"].([]any)
	if len(dims) != 1 {
		t.Fatalf("expected 1 review dimension, got %+v", dims)
	}
	d := dims[0].(map[string]any)
	if d["name"] != "security-review" || d["severity"] != "high" || d["model"] != "opus" {
		t.Errorf("unexpected review dimension metadata: %+v", d)
	}
	triggers, ok := d["triggers"].([]any)
	if !ok || len(triggers) != 1 || triggers[0] != "**/*.go" {
		t.Errorf("unexpected triggers: %+v", d["triggers"])
	}
	// RULING 1: metadata-only. No rendered/body content, no extra keys.
	allowed := map[string]bool{"name": true, "severity": true, "description": true, "triggers": true, "model": true, "path": true}
	for k := range d {
		if !allowed[k] {
			t.Errorf("unexpected key %q in reviewDimensions entry (metadata-only surface): %+v", k, d)
		}
	}
	if got, ok := d["path"].(string); !ok || got == "" {
		t.Errorf("expected non-empty path, got %v", d["path"])
	}
}

func TestHardenPrepare_LoadsCopilotInstructions(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".github", "instructions", "go-style.instructions.md"),
		"---\napplyTo: \"**/*.go\"\n---\nBody content.\n")

	out, err := hardenPrepare(root, root, HardenPrepareIn{
		FailureText:     "boom",
		Skill:           "ship",
		SkipConfigCheck: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	manifest := readHardenManifest(t, out.ManifestPath)
	surfaces := manifest["surfaces"].(map[string]any)
	insts := surfaces["copilotInstructions"].([]any)
	if len(insts) != 1 {
		t.Fatalf("expected 1 copilot instruction, got %+v", insts)
	}
	inst := insts[0].(map[string]any)
	if inst["applyTo"] != "**/*.go" || inst["name"] != "go-style" {
		t.Errorf("unexpected copilot instruction: %+v", inst)
	}
}

func TestHardenPrepare_CopilotInstructionsToleratesMissingFrontmatter(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".github", "instructions", "no-fm.instructions.md"), "Just a body, no frontmatter.\n")

	out, err := hardenPrepare(root, root, HardenPrepareIn{
		FailureText:     "boom",
		Skill:           "ship",
		SkipConfigCheck: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	manifest := readHardenManifest(t, out.ManifestPath)
	surfaces := manifest["surfaces"].(map[string]any)
	insts := surfaces["copilotInstructions"].([]any)
	if len(insts) != 1 {
		t.Fatalf("expected 1 copilot instruction despite missing frontmatter, got %+v", insts)
	}
	inst := insts[0].(map[string]any)
	if inst["applyTo"] != "" || inst["name"] != "no-fm" {
		t.Errorf("unexpected copilot instruction for missing-frontmatter file: %+v", inst)
	}
	// Missing frontmatter is not an error for copilot-instructions (unlike
	// review-dimensions). Filter out the environment-dependent
	// error-report-skill soft-fail (see TestResolveErrorReportSkill_SoftFailsWhenAbsent).
	errs := manifest["errors"].([]any)
	if remaining := filterOutSurface(errs, "error-report-skill"); len(remaining) != 0 {
		t.Errorf("expected no surface-load errors for missing frontmatter, got %+v", remaining)
	}
}

// ---------------------------------------------------------------------------
// Pipeline state (state.FindAny — branch-agnostic, RULING 4)
// ---------------------------------------------------------------------------

func TestHardenPrepare_PipelineStateIsBranchAgnostic(t *testing.T) {
	root := t.TempDir()

	// A ship-state file for a DIFFERENT branch than any branch this test
	// process is on — state.FindAny must still find it (no branch filter).
	st, err := state.Init(root, "ship", "some-other-branch", "")
	if err != nil {
		t.Fatalf("state.Init: %v", err)
	}
	st.Data["paused"] = true
	st.Data["currentStep"] = "step-3"
	st.Data["lastFailedStep"] = "step-2"
	if err := state.Write(st); err != nil {
		t.Fatalf("state.Write: %v", err)
	}

	execSt, err := state.Init(root, "execute", "yet-another-branch", "")
	if err != nil {
		t.Fatalf("state.Init: %v", err)
	}
	execSt.Data["failedTask"] = "task-7"
	execSt.Data["failedWave"] = float64(2)
	if err := state.Write(execSt); err != nil {
		t.Fatalf("state.Write: %v", err)
	}

	out, err := hardenPrepare(root, root, HardenPrepareIn{
		FailureText:     "boom",
		Skill:           "ship",
		SkipConfigCheck: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	manifest := readHardenManifest(t, out.ManifestPath)
	pipeline := manifest["pipeline"].(map[string]any)

	shipState := pipeline["shipState"].(map[string]any)
	if shipState["paused"] != true {
		t.Errorf("shipState.paused = %v, want true", shipState["paused"])
	}
	if shipState["currentStep"] != "step-3" {
		t.Errorf("shipState.currentStep = %v, want step-3", shipState["currentStep"])
	}
	if shipState["lastFailedStep"] != "step-2" {
		t.Errorf("shipState.lastFailedStep = %v, want step-2", shipState["lastFailedStep"])
	}

	executeState := pipeline["executeState"].(map[string]any)
	if executeState["failedTask"] != "task-7" {
		t.Errorf("executeState.failedTask = %v, want task-7", executeState["failedTask"])
	}
	if executeState["failedWave"] != float64(2) {
		t.Errorf("executeState.failedWave = %v, want 2", executeState["failedWave"])
	}
}

func TestHardenPrepare_PipelineStateAbsentIsNull(t *testing.T) {
	root := t.TempDir()
	out, err := hardenPrepare(root, root, HardenPrepareIn{
		FailureText:     "boom",
		Skill:           "ship",
		SkipConfigCheck: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	manifest := readHardenManifest(t, out.ManifestPath)
	pipeline := manifest["pipeline"].(map[string]any)
	if pipeline["shipState"] != nil {
		t.Errorf("shipState = %v, want nil with no state files", pipeline["shipState"])
	}
	if pipeline["executeState"] != nil {
		t.Errorf("executeState = %v, want nil with no state files", pipeline["executeState"])
	}
	if _, ok := pipeline["issues"]; ok {
		t.Errorf("pipeline.issues present with no state files, want key omitted entirely")
	}
}

func TestHardenPrepare_PipelineIssuesMergedFromBothStates(t *testing.T) {
	root := t.TempDir()

	st, err := state.Init(root, "ship", "some-branch", "")
	if err != nil {
		t.Fatalf("state.Init: %v", err)
	}
	execAppendIssue(st.Data, StateIssue{
		Step:     "commit",
		Severity: "warning",
		Category: "step-fail",
		Summary:  "Commit message needed manual review",
	})
	if err := state.Write(st); err != nil {
		t.Fatalf("state.Write: %v", err)
	}

	execSt, err := state.Init(root, "execute", "some-branch", "")
	if err != nil {
		t.Fatalf("state.Init: %v", err)
	}
	execAppendIssue(execSt.Data, StateIssue{
		Wave:     2,
		TaskID:   "T4",
		Severity: "error",
		Category: "task-fail",
		Summary:  "Build failed: missing import",
	})
	if err := state.Write(execSt); err != nil {
		t.Fatalf("state.Write: %v", err)
	}

	out, err := hardenPrepare(root, root, HardenPrepareIn{
		FailureText:     "boom",
		Skill:           "ship",
		SkipConfigCheck: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	manifest := readHardenManifest(t, out.ManifestPath)
	pipeline := manifest["pipeline"].(map[string]any)

	issuesRaw, ok := pipeline["issues"]
	if !ok {
		t.Fatalf("pipeline.issues missing, want merged issues from ship + execute state")
	}
	issues := issuesRaw.([]any)
	if len(issues) != 2 {
		t.Fatalf("len(pipeline.issues) = %d, want 2", len(issues))
	}

	// Merge order: ship-state issues first, then execute-state issues,
	// matching readHardenPipelineState's read order.
	shipIssue := issues[0].(map[string]any)
	if shipIssue["step"] != "commit" || shipIssue["severity"] != "warning" {
		t.Errorf("issues[0] = %+v, want ship-state issue (step=commit, severity=warning)", shipIssue)
	}

	execIssue := issues[1].(map[string]any)
	if execIssue["wave"] != float64(2) {
		t.Errorf("issues[1].wave = %v, want 2", execIssue["wave"])
	}
	if execIssue["taskId"] != "T4" {
		t.Errorf("issues[1].taskId = %v, want T4", execIssue["taskId"])
	}
	if execIssue["severity"] != "error" || execIssue["category"] != "task-fail" {
		t.Errorf("issues[1] = %+v, want severity=error category=task-fail", execIssue)
	}
	if execIssue["summary"] != "Build failed: missing import" {
		t.Errorf("issues[1].summary = %v, want %q", execIssue["summary"], "Build failed: missing import")
	}

	// Typed failedTask/failedWave read alongside issues[] in the same state
	// file, unaffected by the merge.
	executeState := pipeline["executeState"].(map[string]any)
	if executeState["failedTask"] != nil {
		t.Errorf("executeState.failedTask = %v, want omitted (not set in this fixture)", executeState["failedTask"])
	}
}

func TestHardenPrepare_PipelineIssuesOmittedWhenNoIssuesKey(t *testing.T) {
	root := t.TempDir()

	// Ship/execute state exist (backward-compat AC) but neither ever called
	// execAppendIssue, so data["issues"] is absent entirely — the state
	// shape predating Task 17's issues[] accumulator.
	st, err := state.Init(root, "ship", "some-branch", "")
	if err != nil {
		t.Fatalf("state.Init: %v", err)
	}
	st.Data["paused"] = true
	if err := state.Write(st); err != nil {
		t.Fatalf("state.Write: %v", err)
	}

	execSt, err := state.Init(root, "execute", "some-branch", "")
	if err != nil {
		t.Fatalf("state.Init: %v", err)
	}
	execSt.Data["failedTask"] = "task-9"
	execSt.Data["failedWave"] = float64(3)
	if err := state.Write(execSt); err != nil {
		t.Fatalf("state.Write: %v", err)
	}

	out, err := hardenPrepare(root, root, HardenPrepareIn{
		FailureText:     "boom",
		Skill:           "ship",
		SkipConfigCheck: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	manifest := readHardenManifest(t, out.ManifestPath)
	pipeline := manifest["pipeline"].(map[string]any)

	if _, ok := pipeline["issues"]; ok {
		t.Errorf("pipeline.issues present with no issues[] in either state, want key omitted")
	}

	executeState := pipeline["executeState"].(map[string]any)
	if executeState["failedTask"] != "task-9" {
		t.Errorf("executeState.failedTask = %v, want task-9", executeState["failedTask"])
	}
	if executeState["failedWave"] != float64(3) {
		t.Errorf("executeState.failedWave = %v, want 3", executeState["failedWave"])
	}
}

// ---------------------------------------------------------------------------
// CLI evidence (cli-executions.jsonl wired into the manifest)
// ---------------------------------------------------------------------------

// TestHardenPrepare_CLIEvidence covers both halves of wiring
// readRecentCLIEvidence into hardenManifest: (1) entries are filtered to the
// resolved branch and (2) a missing evidence file is a no-op, not an error.
// initGitFixture/gitCommit (scaffold_test.go, same package) give a real git
// repo so gitx.CurrentBranch(contentRoot) resolves a known branch ("main")
// instead of failing outside a repo, as it does in this file's other
// fixtures (those rely on branch=="" skipping the filter entirely).
func TestHardenPrepare_CLIEvidence(t *testing.T) {
	t.Run("FiltersToCurrentBranch", func(t *testing.T) {
		root := t.TempDir()
		initGitFixture(t, root)
		gitCommit(t, root, "c1")

		entries := []CLIEvidenceEntry{
			{
				Timestamp: "2026-09-11T00:00:00Z", Pipeline: "ship", Step: "commit",
				Branch: "main", Command: "git status", ExitCode: 0, OutputHead: "clean",
			},
			{
				Timestamp: "2026-09-11T00:00:01Z", Pipeline: "execute",
				Wave:   func() *int { v := 1; return &v }(),
				Branch: "feature-x", Command: "npm test", ExitCode: 1, OutputHead: "FAIL",
			},
			{
				Timestamp: "2026-09-11T00:00:02Z", Pipeline: "ship", Step: "test",
				Branch: "main", Command: "go build ./...", ExitCode: 0, OutputHead: "",
			},
		}
		for _, e := range entries {
			if err := appendCLIEvidence(root, e); err != nil {
				t.Fatalf("appendCLIEvidence: %v", err)
			}
		}

		out, err := hardenPrepare(root, root, HardenPrepareIn{
			FailureText:     "boom",
			Skill:           "ship",
			SkipConfigCheck: true,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		manifest := readHardenManifest(t, out.ManifestPath)

		raw, ok := manifest["cliEvidence"]
		if !ok {
			t.Fatal("cliEvidence key missing from manifest, want present with main-branch entries")
		}
		list, ok := raw.([]any)
		if !ok {
			t.Fatalf("cliEvidence is not an array: %T", raw)
		}
		if len(list) != 2 {
			t.Fatalf("expected 2 main-branch entries, got %d: %+v", len(list), list)
		}
		first := list[0].(map[string]any)
		if first["command"] != "git status" || first["branch"] != "main" {
			t.Errorf("cliEvidence[0] = %+v, want the first main-branch entry (git status)", first)
		}
		second := list[1].(map[string]any)
		if second["command"] != "go build ./..." || second["branch"] != "main" {
			t.Errorf("cliEvidence[1] = %+v, want the second main-branch entry (go build ./...)", second)
		}
		for _, item := range list {
			m := item.(map[string]any)
			if m["branch"] == "feature-x" {
				t.Errorf("cliEvidence leaked a non-matching-branch entry: %+v", m)
			}
		}
	})

	t.Run("FiltersBranchBeforeTakingLast20", func(t *testing.T) {
		// 25 main-branch entries followed by 25 other-branch entries: the
		// last 20 file entries are all other-branch, but the manifest must
		// still carry the last 20 main-branch ones.
		root := t.TempDir()
		initGitFixture(t, root)
		gitCommit(t, root, "c1")

		for _, br := range []string{"main", "feature-x"} {
			for i := 0; i < 25; i++ {
				e := CLIEvidenceEntry{
					Timestamp: fmt.Sprintf("2026-09-11T00:%02d:00Z", i), Pipeline: "ship",
					Branch: br, Command: fmt.Sprintf("%s-%02d", br, i),
				}
				if err := appendCLIEvidence(root, e); err != nil {
					t.Fatalf("appendCLIEvidence: %v", err)
				}
			}
		}

		out, err := hardenPrepare(root, root, HardenPrepareIn{
			FailureText:     "boom",
			Skill:           "ship",
			SkipConfigCheck: true,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		manifest := readHardenManifest(t, out.ManifestPath)
		list, _ := manifest["cliEvidence"].([]any)
		if len(list) != 20 {
			t.Fatalf("expected 20 main-branch entries, got %d: %+v", len(list), list)
		}
		first := list[0].(map[string]any)
		last := list[19].(map[string]any)
		if first["command"] != "main-05" || last["command"] != "main-24" {
			t.Errorf("cliEvidence = %v .. %v, want main-05 .. main-24", first["command"], last["command"])
		}
	})

	t.Run("MissingEvidenceFileOmitsFieldWithoutError", func(t *testing.T) {
		root := t.TempDir()
		initGitFixture(t, root)
		gitCommit(t, root, "c1")

		// Deliberately no cli-executions.jsonl written under this root.
		out, err := hardenPrepare(root, root, HardenPrepareIn{
			FailureText:     "boom",
			Skill:           "ship",
			SkipConfigCheck: true,
		})
		if err != nil {
			t.Fatalf("unexpected error with no evidence file present: %v", err)
		}
		manifest := readHardenManifest(t, out.ManifestPath)
		if raw, ok := manifest["cliEvidence"]; ok {
			if list, ok := raw.([]any); !ok || len(list) != 0 {
				t.Errorf("cliEvidence = %+v (present, %T), want omitted or empty when no evidence file exists", raw, raw)
			}
		}
	})
}

// ---------------------------------------------------------------------------
// Pure-function helpers
// ---------------------------------------------------------------------------

func TestIssueLabelNames_MixedStringAndObjectLabels(t *testing.T) {
	raw := []any{
		"good-first-issue",
		map[string]any{"name": "mcp-failure", "color": "ff0000"},
		map[string]any{"noNameField": true},
		42, // not a string or {name} object — silently dropped
	}
	got := issueLabelNames(raw)
	want := []string{"good-first-issue", "mcp-failure"}
	if len(got) != len(want) {
		t.Fatalf("issueLabelNames = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("issueLabelNames[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	if !containsStr(got, "mcp-failure") {
		t.Error("expected containsStr to find mcp-failure label")
	}
}

func TestAnySliceToStrings(t *testing.T) {
	if got := anySliceToStrings([]any{"a", "b", 3, "c"}); len(got) != 3 || got[0] != "a" || got[1] != "b" || got[2] != "c" {
		t.Errorf("anySliceToStrings dropped-non-string case = %+v", got)
	}
	if got := anySliceToStrings("not-an-array"); len(got) != 0 {
		t.Errorf("anySliceToStrings(non-array) = %+v, want empty", got)
	}
	if got := anySliceToStrings(nil); len(got) != 0 {
		t.Errorf("anySliceToStrings(nil) = %+v, want empty", got)
	}
}

// TestGuardrailsPreflight_PassesWithNoConfig pins that no config.toml gives
// no findings and no error.
func TestGuardrailsPreflight_PassesWithNoConfig(t *testing.T) {
	root := t.TempDir()
	if findings, err := guardrailsPreflight(root); err != nil || len(findings) != 0 {
		t.Errorf("expected no findings and no error with no config, got %+v, %v", findings, err)
	}
}

// TestDimensionsPreflight_PassesWithNoDir pins that a missing
// review-dimensions directory gives no findings and no error.
func TestDimensionsPreflight_PassesWithNoDir(t *testing.T) {
	root := t.TempDir()
	if findings, err := dimensionsPreflight(root); err != nil || len(findings) != 0 {
		t.Errorf("expected no findings and no error with no dimensions dir, got %+v, %v", findings, err)
	}
}

// TestDimensionsPreflight_IDFallsBackToFileStem pins the id of a dimension
// finding: it is the frontmatter name when that is a non-empty string, and the
// file name without ".md" when the name is empty or not a string.
func TestDimensionsPreflight_IDFallsBackToFileStem(t *testing.T) {
	dimFile := func(nameLine string) string {
		return "---\n" +
			nameLine +
			"severity: bogus\n" +
			"description: Check for security issues in the diff.\n" +
			"triggers:\n" +
			"  - \"**/*.go\"\n" +
			"---\n" +
			"# Dimension\n\nLook for injection and secrets in the diff.\n"
	}
	cases := []struct {
		name     string
		nameLine string
		wantID   string
	}{
		{"named", "name: security-review\n", "security-review"},
		{"empty name", "name:\n", "stem"},
		{"empty string name", "name: \"\"\n", "stem"},
		{"name is not a string", "name: 42\n", "stem"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeFile(t, filepath.Join(root, paths.DataDir, "review-dimensions", "stem.md"), dimFile(tc.nameLine))

			findings, err := dimensionsPreflight(root)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(findings) == 0 {
				t.Fatal("want at least one finding for the bogus severity, got none")
			}
			for _, f := range findings {
				if f.ID != tc.wantID {
					t.Errorf("finding id = %q, want %q (finding %+v)", f.ID, tc.wantID, f)
				}
			}
		})
	}
}

// TestGuardrailDescriptions pins each branch of guardrailDescriptions: a
// missing section or a section with no guardrails key gives an empty map,
// entries that are not objects or have no string id or description are left
// out, and a config that does not parse is an InfraError with a Cause.
func TestGuardrailDescriptions(t *testing.T) {
	t.Run("missing section", func(t *testing.T) {
		root := t.TempDir()
		got, err := guardrailDescriptions(root, "plan")
		if err != nil || got == nil || len(got) != 0 {
			t.Fatalf("want an empty non-nil map and no error, got %#v, %v", got, err)
		}
	})

	t.Run("section without a guardrails key", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, filepath.Join(root, paths.DataDir, "config.toml"), "[plan]\nother = 1\n")
		got, err := guardrailDescriptions(root, "plan")
		if err != nil || got == nil || len(got) != 0 {
			t.Fatalf("want an empty non-nil map and no error, got %#v, %v", got, err)
		}
	})

	t.Run("array form skips entries that are not objects or have no string id", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, filepath.Join(root, paths.DataDir, "config.toml"), ""+
			"[plan]\n"+
			"guardrails = [1, {description = \"x\"}, {id = 7, description = \"y\"}, {id = \"ok\", description = \"d\"}]\n")
		got, err := guardrailDescriptions(root, "plan")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !reflect.DeepEqual(got, map[string]string{"ok": "d"}) {
			t.Errorf("got %+v, want only ok", got)
		}
	})

	t.Run("maps id to description", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, filepath.Join(root, paths.DataDir, "config.toml"), ""+
			"[plan.guardrails.rule-a]\n"+
			"description = \"first\"\n\n"+
			"[plan.guardrails.rule-b]\n"+
			"description = 42\n")
		got, err := guardrailDescriptions(root, "plan")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !reflect.DeepEqual(got, map[string]string{"rule-a": "first"}) {
			t.Errorf("got %+v, want only rule-a", got)
		}
	})

	t.Run("config does not parse", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, filepath.Join(root, paths.DataDir, "config.toml"), "[plan\nthis is = = not toml\n")
		_, err := guardrailDescriptions(root, "plan")
		var infraErr *mcpserver.InfraError
		if !errorsAsInfraError(err, &infraErr) {
			t.Fatalf("expected *mcpserver.InfraError, got %T: %v", err, err)
		}
		if infraErr.Cause == nil || !containsSubstr(infraErr.Suggestion, "[plan] section is valid TOML") {
			t.Errorf("unexpected InfraError: %+v", infraErr)
		}
	})
}

// TestBytesOver pins bytesOver at, below and above guardrailDescMaxBytes.
func TestBytesOver(t *testing.T) {
	cases := map[int]int{0: 0, guardrailDescMaxBytes - 1: 0, guardrailDescMaxBytes: 0, guardrailDescMaxBytes + 1: 1, 1474: 450}
	for n, want := range cases {
		if got := bytesOver(strings.Repeat("a", n)); got != want {
			t.Errorf("bytesOver(%d bytes) = %d, want %d", n, got, want)
		}
	}
}

// TestHardenPrepare_ErrorReportSurfaceResolvesShippedFile pins that the
// error-report-skill surface points at a file the plugin actually ships
// (skills/error-report/SKILL.md) under CLAUDE_PLUGIN_ROOT, so an installed
// plugin gets a path and no load error. The binary itself runs from
// ~/.sdlc-cache/bin, so walking up from the executable cannot find it.
func TestHardenPrepare_ErrorReportSurfaceResolvesShippedFile(t *testing.T) {
	pluginRoot, err := filepath.Abs(filepath.Join("..", "..", "plugins", "sdlc"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_PLUGIN_ROOT", pluginRoot)

	root := t.TempDir()
	out, err := hardenPrepare(root, root, HardenPrepareIn{
		FailureText:     "boom",
		Skill:           "ship",
		SkipConfigCheck: true,
	})
	if err != nil {
		t.Fatalf("hardenPrepare: %v", err)
	}
	manifest := readHardenManifest(t, out.ManifestPath)

	surfaces := manifest["surfaces"].(map[string]any)
	want := filepath.Join(pluginRoot, "skills", "error-report", "SKILL.md")
	if surfaces["errorReportSkillPath"] != want {
		t.Errorf("errorReportSkillPath = %v, want %q", surfaces["errorReportSkillPath"], want)
	}
	errs, _ := manifest["errors"].([]any)
	if len(errs) != len(filterOutSurface(errs, "error-report-skill")) {
		t.Errorf("expected no error-report-skill load error, got %+v", errs)
	}
}

func TestResolveErrorReportSkill_SoftFailsWhenAbsent(t *testing.T) {
	// An empty CLAUDE_PLUGIN_ROOT (no plugin files) and a test binary far
	// from any plugin tree: nothing to resolve.
	t.Setenv("CLAUDE_PLUGIN_ROOT", t.TempDir())
	var errs []surfaceLoadError
	got := resolveErrorReportSkill(&errs)
	// Must soft-fail: empty string, one recorded error, no panic.
	if got != "" {
		t.Errorf("resolveErrorReportSkill = %q, want empty string when the target file does not exist", got)
	}
	if len(errs) != 1 {
		t.Fatalf("expected exactly 1 surface load error, got %+v", errs)
	}
	if errs[0].Surface != "error-report-skill" {
		t.Errorf("errs[0].Surface = %q, want error-report-skill", errs[0].Surface)
	}
}

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

func errorsAsDataError(err error, target **mcpserver.DataError) bool {
	de, ok := err.(*mcpserver.DataError)
	if !ok {
		return false
	}
	*target = de
	return true
}

func errorsAsDomainError(err error, target **mcpserver.DomainError) bool {
	de, ok := err.(*mcpserver.DomainError)
	if !ok {
		return false
	}
	*target = de
	return true
}

// errorsAsInfraError reports whether err is a *mcpserver.InfraError and, when
// it is, stores it in target.
func errorsAsInfraError(err error, target **mcpserver.InfraError) bool {
	ie, ok := err.(*mcpserver.InfraError)
	if !ok {
		return false
	}
	*target = ie
	return true
}

// redirectTempDir points the OS temporary directory (TMPDIR) at a fresh
// directory under t.TempDir for the rest of the test and returns that
// directory. A call that writes a manifest through os.MkdirTemp("", ...)
// then writes inside the test directory, which the test framework removes.
func redirectTempDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	return dir
}

// assertNoHardenManifestDir fails the test when tmpDir holds a directory that
// hardenPrepare creates for a manifest (sdlc-harden-*). It proves that a run
// which stops with an error wrote no manifest.
func assertNoHardenManifestDir(t *testing.T, tmpDir string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(tmpDir, "sdlc-harden-*"))
	if err != nil {
		t.Fatalf("glob manifest dirs: %v", err)
	}
	if len(matches) != 0 {
		t.Errorf("a stopped run must write no manifest, found %v", matches)
	}
}

// manifestPreflightFindings reads the manifest at path and returns its
// preflightFindings entries as decoded JSON objects. It fails the test when
// the key is missing or is not an array of objects.
func manifestPreflightFindings(t *testing.T, path string) []map[string]any {
	t.Helper()
	raw, ok := readHardenManifest(t, path)["preflightFindings"].([]any)
	if !ok {
		t.Fatal("manifest preflightFindings is missing or not an array")
	}
	out := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		m, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("preflightFindings entry is not an object: %#v", item)
		}
		out = append(out, m)
	}
	return out
}

func containsSubstr(s, substr string) bool {
	return len(s) >= len(substr) && (substr == "" || indexOf(s, substr) >= 0)
}

func indexOf(s, substr string) int {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

// filterOutSurface drops every {surface,message} entry (decoded as
// map[string]any) whose "surface" field equals the given name. Used to
// exclude the environment-dependent error-report-skill soft-fail (this test
// binary has no real plugin installation on disk) from assertions about the
// surfaces a given test fixture actually exercises.
func filterOutSurface(errs []any, surface string) []any {
	var out []any
	for _, e := range errs {
		m, ok := e.(map[string]any)
		if !ok || m["surface"] != surface {
			out = append(out, e)
		}
	}
	return out
}

func readHardenManifest(t *testing.T, path string) map[string]any {
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

// keep time import used even if a future edit trims a test that needed it directly.
var _ = time.Now
