package prtemplate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
	"github.com/rnagrodzki/sdlc-plugin/internal/setupmeta"
)

// ---------------------------------------------------------------------------
// SETUP_SECTIONS snapshot test — order + IDs must match Node.js source
// ---------------------------------------------------------------------------

// TestSectionsSnapshot verifies that Sections() returns the exact frozen IDs
// in the exact order matching scripts/lib/setup-sections.js SETUP_SECTIONS.
// The expected list is hardcoded, NOT derived from Sections() itself.
func TestSectionsSnapshot(t *testing.T) {
	expected := []string{
		"version",
		"ship",
		"jira",
		"review",
		"received-review",
		"commit",
		"pr",
		"pr-labels",
		"review-dimensions",
		"pr-template",
		"plan-template",
		"plan-guardrails",
		"execution-guardrails",
		"openspec-block",
	}

	sections := setupmeta.Sections()
	if len(sections) != len(expected) {
		t.Fatalf("Sections() returned %d sections, want %d", len(sections), len(expected))
	}

	for i, want := range expected {
		got := sections[i].ID
		if got != want {
			t.Errorf("Sections()[%d].ID = %q, want %q", i, got, want)
		}
	}
}

// TestSectionsLabelsMatchIDs verifies that every section's Label equals its ID.
// This matches the Node.js source where label and id are always identical.
func TestSectionsLabelsMatchIDs(t *testing.T) {
	for _, s := range setupmeta.Sections() {
		if s.Label != s.ID {
			t.Errorf("Section %q has Label %q (expected Label == ID)", s.ID, s.Label)
		}
	}
}

// TestShipFieldsReferenceIdentity verifies that the ship section's Fields
// slice references the package-level ShipFields (Go equivalent of the JS
// identity check `_shipEntry.fields === SHIP_FIELDS`).
func TestShipFieldsReferenceIdentity(t *testing.T) {
	sections := setupmeta.Sections()
	var ship *setupmeta.Section
	for i := range sections {
		if sections[i].ID == "ship" {
			ship = &sections[i]
			break
		}
	}
	if ship == nil {
		t.Fatal("no section with id 'ship' found")
	}
	if len(ship.Fields) != len(setupmeta.ShipFields) {
		t.Fatalf("ship.Fields length %d != ShipFields length %d", len(ship.Fields), len(setupmeta.ShipFields))
	}
	// Verify backing-array identity: the ship section's Fields slice must
	// share the same underlying array as the package-level ShipFields.
	if &ship.Fields[0] != &setupmeta.ShipFields[0] {
		t.Error("ship.Fields does not share backing array with ShipFields — invariant broken")
	}
}

// TestPrSectionHasFields verifies that the 'pr' section carries fields
// despite also having a delegatedTo value (unlike 'commit' which has
// delegatedTo + empty fields).
func TestPrSectionHasFields(t *testing.T) {
	sections := setupmeta.Sections()
	for _, s := range sections {
		if s.ID == "pr" {
			if len(s.Fields) != 2 {
				t.Errorf("pr section has %d fields, want 2", len(s.Fields))
			}
			if s.DelegatedTo == "" {
				t.Error("pr section should have a non-empty DelegatedTo")
			}
			return
		}
	}
	t.Fatal("no section with id 'pr' found")
}

// TestSectionCount verifies the total number of sections (14).
func TestSectionCount(t *testing.T) {
	if got := len(setupmeta.Sections()); got != 14 {
		t.Errorf("Sections() returned %d sections, want 14", got)
	}
}

// ---------------------------------------------------------------------------
// PR template resolution tests
// ---------------------------------------------------------------------------

func TestResolve_Canonical(t *testing.T) {
	root := t.TempDir()
	sdlcDir := filepath.Join(root, paths.DataDir)
	if err := os.MkdirAll(sdlcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "## Summary\nDescribe changes\n\n## Test Plan\nHow to test\n"
	if err := os.WriteFile(filepath.Join(sdlcDir, "pr-template.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	tmpl, err := Resolve(root)
	if err != nil {
		t.Fatal(err)
	}
	if tmpl == nil {
		t.Fatal("Resolve returned nil, want template")
	}
	if tmpl.Legacy {
		t.Error("expected Legacy=false for canonical path")
	}
	if tmpl.Content != content {
		t.Errorf("Content = %q, want %q", tmpl.Content, content)
	}
	if len(tmpl.Headings) != 2 {
		t.Fatalf("Headings count = %d, want 2", len(tmpl.Headings))
	}
	if tmpl.Headings[0] != "Summary" || tmpl.Headings[1] != "Test Plan" {
		t.Errorf("Headings = %v, want [Summary, Test Plan]", tmpl.Headings)
	}
}

func TestResolve_LegacyFallback(t *testing.T) {
	root := t.TempDir()
	claudeDir := filepath.Join(root, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "## Overview\nLegacy template\n"
	if err := os.WriteFile(filepath.Join(claudeDir, "pr-template.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	tmpl, err := Resolve(root)
	if err != nil {
		t.Fatal(err)
	}
	if tmpl == nil {
		t.Fatal("Resolve returned nil, want template")
	}
	if !tmpl.Legacy {
		t.Error("expected Legacy=true for legacy path")
	}
	if tmpl.Content != content {
		t.Errorf("Content = %q, want %q", tmpl.Content, content)
	}
}

func TestResolve_CanonicalTakesPrecedence(t *testing.T) {
	root := t.TempDir()
	sdlcDir := filepath.Join(root, paths.DataDir)
	claudeDir := filepath.Join(root, ".claude")
	if err := os.MkdirAll(sdlcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	canonicalContent := "## Canonical\nThis wins\n"
	legacyContent := "## Legacy\nThis loses\n"
	if err := os.WriteFile(filepath.Join(sdlcDir, "pr-template.md"), []byte(canonicalContent), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(claudeDir, "pr-template.md"), []byte(legacyContent), 0o644); err != nil {
		t.Fatal(err)
	}

	tmpl, err := Resolve(root)
	if err != nil {
		t.Fatal(err)
	}
	if tmpl == nil {
		t.Fatal("Resolve returned nil")
	}
	if tmpl.Legacy {
		t.Error("expected Legacy=false when canonical exists")
	}
	if tmpl.Content != canonicalContent {
		t.Error("expected canonical content, got legacy")
	}
}

func TestResolve_EmptyCanonicalTakesPrecedence(t *testing.T) {
	root := t.TempDir()
	sdlcDir := filepath.Join(root, paths.DataDir)
	claudeDir := filepath.Join(root, ".claude")
	if err := os.MkdirAll(sdlcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Canonical exists but is empty; legacy has content.
	if err := os.WriteFile(filepath.Join(sdlcDir, "pr-template.md"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(claudeDir, "pr-template.md"), []byte("## Legacy\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tmpl, err := Resolve(root)
	if err != nil {
		t.Fatal(err)
	}
	if tmpl == nil {
		t.Fatal("Resolve returned nil; empty canonical file should still resolve")
	}
	if tmpl.Legacy {
		t.Error("expected Legacy=false; empty canonical should take precedence")
	}
	if tmpl.Content != "" {
		t.Errorf("expected empty Content, got %q", tmpl.Content)
	}
}

func TestResolve_NoTemplate(t *testing.T) {
	root := t.TempDir()
	tmpl, err := Resolve(root)
	if err != nil {
		t.Fatal(err)
	}
	if tmpl != nil {
		t.Errorf("expected nil template when no file exists, got %+v", tmpl)
	}
}

// ---------------------------------------------------------------------------
// ValidateBody tests
// ---------------------------------------------------------------------------

func TestValidateBody_AllSectionsPresent(t *testing.T) {
	tmpl := &Template{
		Headings: []string{"Summary", "Test Plan"},
	}
	body := "## Summary\nSome changes\n\n## Test Plan\nUnit tests added\n"
	issues := ValidateBody(body, tmpl)
	if len(issues) != 0 {
		t.Errorf("expected no issues, got %v", issues)
	}
}

func TestValidateBody_MissingSections(t *testing.T) {
	tmpl := &Template{
		Headings: []string{"Summary", "Test Plan", "JIRA Ticket"},
	}
	body := "## Summary\nSome changes\n"
	issues := ValidateBody(body, tmpl)
	if len(issues) != 2 {
		t.Fatalf("expected 2 issues, got %d: %v", len(issues), issues)
	}
	if !strings.Contains(issues[0], "Test Plan") {
		t.Errorf("issues[0] should mention 'Test Plan': %s", issues[0])
	}
	if !strings.Contains(issues[1], "JIRA Ticket") {
		t.Errorf("issues[1] should mention 'JIRA Ticket': %s", issues[1])
	}
}

func TestValidateBody_NilTemplate(t *testing.T) {
	issues := ValidateBody("any body", nil)
	if issues != nil {
		t.Errorf("expected nil issues for nil template, got %v", issues)
	}
}

func TestValidateBody_EmptyHeadings(t *testing.T) {
	tmpl := &Template{Headings: nil}
	issues := ValidateBody("any body", tmpl)
	if issues != nil {
		t.Errorf("expected nil issues for template with no headings, got %v", issues)
	}
}

func TestValidateBody_EmptyBody(t *testing.T) {
	tmpl := &Template{
		Headings: []string{"Summary"},
	}
	issues := ValidateBody("", tmpl)
	if len(issues) != 1 {
		t.Fatalf("expected 1 issue, got %d: %v", len(issues), issues)
	}
	if !strings.Contains(issues[0], "Summary") {
		t.Errorf("issue should mention 'Summary': %s", issues[0])
	}
}

// TestValidateBody_ExtraHeadingsOK verifies that the body may contain
// headings not in the template without generating issues.
func TestValidateBody_ExtraHeadingsOK(t *testing.T) {
	tmpl := &Template{
		Headings: []string{"Summary"},
	}
	body := "## Summary\nDone\n\n## Bonus Section\nExtra\n"
	issues := ValidateBody(body, tmpl)
	if len(issues) != 0 {
		t.Errorf("expected no issues for extra headings, got %v", issues)
	}
}

// ---------------------------------------------------------------------------
// extractHeadings unit test
// ---------------------------------------------------------------------------

func TestExtractHeadings(t *testing.T) {
	text := "# Title\n## Summary\ntext\n### Sub\n## Test Plan  \nmore\n##NoSpace\n## \n"
	got := extractHeadings(text)
	want := []string{"Summary", "Test Plan"}
	if len(got) != len(want) {
		t.Fatalf("extractHeadings returned %d headings, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("headings[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
