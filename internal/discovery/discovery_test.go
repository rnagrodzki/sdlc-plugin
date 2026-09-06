package discovery

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll %s: %v", dir, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile %s: %v", path, err)
	}
}

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll %s: %v", dir, err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("WriteFile %s: %v", path, err)
	}
}

// makeGoodFixture creates a complete valid project structure that passes all
// PD1–PD16 checks. Returns the root directory path.
func makeGoodFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	// .claude-plugin/marketplace.json
	writeJSON(t, filepath.Join(root, ".claude-plugin", "marketplace.json"), map[string]any{
		"$schema": "https://anthropic.com/claude-code/marketplace.schema.json",
		"name":    "test-project",
		"plugins": []map[string]any{
			{"name": "test-plugin", "source": "."},
		},
	})

	// .claude-plugin/plugin.json (source "." means same root)
	writeJSON(t, filepath.Join(root, ".claude-plugin", "plugin.json"), map[string]any{
		"name":        "test-plugin",
		"description": "A test plugin",
		"version":     "1.0.0",
	})

	// commands/do-thing.md with valid frontmatter
	writeFile(t, filepath.Join(root, "commands", "do-thing.md"),
		"---\ndescription: Does a thing\n---\nContent here\n")

	// skills/my-skill/SKILL.md with valid frontmatter
	writeFile(t, filepath.Join(root, "skills", "my-skill", "SKILL.md"),
		"---\nname: my-skill\ndescription: A test skill\n---\nContent here\n")

	// hooks/hooks.json — valid JSON
	writeJSON(t, filepath.Join(root, "hooks", "hooks.json"), map[string]any{})

	// agents/my-agent.md with valid frontmatter
	writeFile(t, filepath.Join(root, "agents", "my-agent.md"),
		"---\nname: my-agent\ndescription: A test agent\ntools: Bash\n---\nContent here\n")

	return root
}

func findByID(findings []Finding, id string) []Finding {
	var result []Finding
	for _, f := range findings {
		if f.ID == id {
			result = append(result, f)
		}
	}
	return result
}

func assertHasFinding(t *testing.T, findings []Finding, id, severity string) {
	t.Helper()
	for _, f := range findings {
		if f.ID == id && f.Severity == severity {
			return
		}
	}
	t.Errorf("expected finding with ID=%q severity=%q, got none", id, severity)
}

func assertNoFinding(t *testing.T, findings []Finding, id string) {
	t.Helper()
	for _, f := range findings {
		if f.ID == id {
			t.Errorf("unexpected finding with ID=%q: %s", id, f.Message)
		}
	}
}

func assertOnlyFindings(t *testing.T, findings []Finding, ids ...string) {
	t.Helper()
	allowed := map[string]bool{}
	for _, id := range ids {
		allowed[id] = true
	}
	for _, f := range findings {
		if !allowed[f.ID] {
			t.Errorf("unexpected finding with ID=%q: %s", f.ID, f.Message)
		}
	}
}

// ---------------------------------------------------------------------------
// Good fixture — all checks pass
// ---------------------------------------------------------------------------

func TestValidateAll_GoodFixture(t *testing.T) {
	root := makeGoodFixture(t)
	findings := ValidateAll(root)
	if len(findings) > 0 {
		for _, f := range findings {
			t.Errorf("unexpected finding: %s (%s): %s [path=%s]", f.ID, f.Severity, f.Message, f.Path)
		}
	}
}

// ---------------------------------------------------------------------------
// PD1 — marketplace.json exists and is valid JSON
// ---------------------------------------------------------------------------

func TestPD1_Missing(t *testing.T) {
	root := t.TempDir()
	findings := ValidateAll(root)
	assertHasFinding(t, findings, "PD1", "error")
	// When PD1 fails, PD2–PD16 skip (produce no findings).
	assertOnlyFindings(t, findings, "PD1")
}

func TestPD1_InvalidJSON(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".claude-plugin", "marketplace.json"), "not json")
	findings := ValidateAll(root)
	assertHasFinding(t, findings, "PD1", "error")
	assertOnlyFindings(t, findings, "PD1")
}

// ---------------------------------------------------------------------------
// PD2 — $schema field present (warning)
// ---------------------------------------------------------------------------

func TestPD2_MissingSchema(t *testing.T) {
	root := makeGoodFixture(t)
	// Rewrite marketplace.json without $schema.
	writeJSON(t, filepath.Join(root, ".claude-plugin", "marketplace.json"), map[string]any{
		"name": "test-project",
		"plugins": []map[string]any{
			{"name": "test-plugin", "source": "."},
		},
	})
	findings := ValidateAll(root)
	assertHasFinding(t, findings, "PD2", "warning")
	assertOnlyFindings(t, findings, "PD2")
}

// ---------------------------------------------------------------------------
// PD3 — marketplace required fields (name + plugins)
// ---------------------------------------------------------------------------

func TestPD3_MissingName(t *testing.T) {
	root := makeGoodFixture(t)
	writeJSON(t, filepath.Join(root, ".claude-plugin", "marketplace.json"), map[string]any{
		"$schema": "https://anthropic.com/claude-code/marketplace.schema.json",
		"plugins": []map[string]any{
			{"name": "test-plugin", "source": "."},
		},
	})
	findings := ValidateAll(root)
	assertHasFinding(t, findings, "PD3", "error")
	assertOnlyFindings(t, findings, "PD3")
}

func TestPD3_MissingPlugins(t *testing.T) {
	root := makeGoodFixture(t)
	writeJSON(t, filepath.Join(root, ".claude-plugin", "marketplace.json"), map[string]any{
		"$schema": "https://anthropic.com/claude-code/marketplace.schema.json",
		"name":    "test-project",
	})
	findings := ValidateAll(root)
	assertHasFinding(t, findings, "PD3", "error")
	// PD4+ skip because plugins list is missing/empty.
	assertOnlyFindings(t, findings, "PD3")
}

// ---------------------------------------------------------------------------
// PD4 — plugin source paths valid
// ---------------------------------------------------------------------------

func TestPD4_SourceNotFound(t *testing.T) {
	root := makeGoodFixture(t)
	writeJSON(t, filepath.Join(root, ".claude-plugin", "marketplace.json"), map[string]any{
		"$schema": "https://anthropic.com/claude-code/marketplace.schema.json",
		"name":    "test-project",
		"plugins": []map[string]any{
			{"name": "nonexistent", "source": "./no-such-dir"},
		},
	})
	findings := ValidateAll(root)
	assertHasFinding(t, findings, "PD4", "error")
	// PD5–PD16 skip because there are no valid plugins.
	assertOnlyFindings(t, findings, "PD4")
}

func TestPD4_MissingPluginJSON(t *testing.T) {
	root := makeGoodFixture(t)
	subDir := filepath.Join(root, "sub-plugin")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeJSON(t, filepath.Join(root, ".claude-plugin", "marketplace.json"), map[string]any{
		"$schema": "https://anthropic.com/claude-code/marketplace.schema.json",
		"name":    "test-project",
		"plugins": []map[string]any{
			{"name": "sub-plugin", "source": "./sub-plugin"},
		},
	})
	findings := ValidateAll(root)
	assertHasFinding(t, findings, "PD4", "error")
	assertOnlyFindings(t, findings, "PD4")
}

// ---------------------------------------------------------------------------
// PD5 — name consistency
// ---------------------------------------------------------------------------

func TestPD5_NameMismatch(t *testing.T) {
	root := makeGoodFixture(t)
	// Marketplace entry says "wrong-name" but plugin.json says "test-plugin".
	writeJSON(t, filepath.Join(root, ".claude-plugin", "marketplace.json"), map[string]any{
		"$schema": "https://anthropic.com/claude-code/marketplace.schema.json",
		"name":    "test-project",
		"plugins": []map[string]any{
			{"name": "wrong-name", "source": "."},
		},
	})
	findings := ValidateAll(root)
	assertHasFinding(t, findings, "PD5", "error")
	assertNoFinding(t, findings, "PD6")
	assertNoFinding(t, findings, "PD7")
}

// ---------------------------------------------------------------------------
// PD6 — plugin.json required fields (name, description, version)
// ---------------------------------------------------------------------------

func TestPD6_MissingDescription(t *testing.T) {
	root := makeGoodFixture(t)
	writeJSON(t, filepath.Join(root, ".claude-plugin", "plugin.json"), map[string]any{
		"name":    "test-plugin",
		"version": "1.0.0",
	})
	findings := ValidateAll(root)
	assertHasFinding(t, findings, "PD6", "error")
	assertNoFinding(t, findings, "PD5")
	assertNoFinding(t, findings, "PD7")
}

// ---------------------------------------------------------------------------
// PD7 — semver format
// ---------------------------------------------------------------------------

func TestPD7_InvalidSemver(t *testing.T) {
	root := makeGoodFixture(t)
	writeJSON(t, filepath.Join(root, ".claude-plugin", "plugin.json"), map[string]any{
		"name":        "test-plugin",
		"description": "A test plugin",
		"version":     "not-semver",
	})
	findings := ValidateAll(root)
	assertHasFinding(t, findings, "PD7", "error")
	assertNoFinding(t, findings, "PD5")
	assertNoFinding(t, findings, "PD6")
}

func TestPD7_ValidPrerelease(t *testing.T) {
	root := makeGoodFixture(t)
	writeJSON(t, filepath.Join(root, ".claude-plugin", "plugin.json"), map[string]any{
		"name":        "test-plugin",
		"description": "A test plugin",
		"version":     "1.0.0-alpha.1",
	})
	findings := ValidateAll(root)
	assertNoFinding(t, findings, "PD7")
}

// ---------------------------------------------------------------------------
// PD8 — commands discoverable (frontmatter with description)
// ---------------------------------------------------------------------------

func TestPD8_MissingFrontmatter(t *testing.T) {
	root := makeGoodFixture(t)
	writeFile(t, filepath.Join(root, "commands", "bad-cmd.md"),
		"No frontmatter here\n")
	findings := ValidateAll(root)
	assertHasFinding(t, findings, "PD8", "error")
	assertNoFinding(t, findings, "PD9")
	assertNoFinding(t, findings, "PD10")
}

func TestPD8_MissingDescription(t *testing.T) {
	root := makeGoodFixture(t)
	writeFile(t, filepath.Join(root, "commands", "no-desc.md"),
		"---\ntitle: some command\n---\nContent\n")
	findings := ValidateAll(root)
	assertHasFinding(t, findings, "PD8", "error")
}

// ---------------------------------------------------------------------------
// PD9 — command skill references valid
// ---------------------------------------------------------------------------

func TestPD9_SkillNotFound(t *testing.T) {
	root := makeGoodFixture(t)
	writeFile(t, filepath.Join(root, "commands", "refs-skill.md"),
		"---\ndescription: Invokes a skill\n---\nInvoke the `nonexistent-skill` skill to do things\n")
	findings := ValidateAll(root)
	assertHasFinding(t, findings, "PD9", "error")
	assertNoFinding(t, findings, "PD8")
	assertNoFinding(t, findings, "PD10")
}

// ---------------------------------------------------------------------------
// PD10 — command script references valid
// ---------------------------------------------------------------------------

func TestPD10_ScriptNotFound(t *testing.T) {
	root := makeGoodFixture(t)
	writeFile(t, filepath.Join(root, "commands", "refs-script.md"),
		"---\ndescription: Runs a script\n---\nRun find . -name 'missing-script.js' to locate it\n")
	findings := ValidateAll(root)
	assertHasFinding(t, findings, "PD10", "error")
	assertNoFinding(t, findings, "PD8")
	assertNoFinding(t, findings, "PD9")
}

// ---------------------------------------------------------------------------
// PD11 — skills discoverable (SKILL.md with name+description)
// ---------------------------------------------------------------------------

func TestPD11_MissingSKILLmd(t *testing.T) {
	root := makeGoodFixture(t)
	// Create a skill directory without SKILL.md.
	if err := os.MkdirAll(filepath.Join(root, "skills", "broken-skill"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Write a placeholder so the dir isn't empty.
	writeFile(t, filepath.Join(root, "skills", "broken-skill", "placeholder.txt"), "x")
	findings := ValidateAll(root)
	assertHasFinding(t, findings, "PD11", "error")
	assertNoFinding(t, findings, "PD12")
	assertNoFinding(t, findings, "PD13")
	assertNoFinding(t, findings, "PD14")
}

func TestPD11_MissingFrontmatterFields(t *testing.T) {
	root := makeGoodFixture(t)
	// Skill with frontmatter missing "name".
	writeFile(t, filepath.Join(root, "skills", "no-name", "SKILL.md"),
		"---\ndescription: A skill without a name\n---\nContent\n")
	findings := ValidateAll(root)
	assertHasFinding(t, findings, "PD11", "error")
	// The good "my-skill" should not trigger PD11.
	if got := len(findByID(findings, "PD11")); got != 1 {
		t.Errorf("expected 1 PD11 finding, got %d", got)
	}
}

// ---------------------------------------------------------------------------
// PD12 — skill supporting files exist
// ---------------------------------------------------------------------------

func TestPD12_SiblingFileNotFound(t *testing.T) {
	root := makeGoodFixture(t)
	writeFile(t, filepath.Join(root, "skills", "has-sibling-ref", "SKILL.md"),
		"---\nname: has-sibling-ref\ndescription: References a sibling\n---\nSee `REFERENCE.md` for details\n")
	findings := ValidateAll(root)
	assertHasFinding(t, findings, "PD12", "error")
	assertNoFinding(t, findings, "PD11")
}

func TestPD12_SiblingFileExists(t *testing.T) {
	root := makeGoodFixture(t)
	writeFile(t, filepath.Join(root, "skills", "has-sibling-ref", "SKILL.md"),
		"---\nname: has-sibling-ref\ndescription: References a sibling\n---\nSee `REFERENCE.md` for details\n")
	writeFile(t, filepath.Join(root, "skills", "has-sibling-ref", "REFERENCE.md"), "Reference content")
	findings := ValidateAll(root)
	assertNoFinding(t, findings, "PD12")
}

func TestPD12_NonSiblingIgnored(t *testing.T) {
	root := makeGoodFixture(t)
	// References to CHANGELOG.md, README.md, etc. should not trigger PD12.
	writeFile(t, filepath.Join(root, "skills", "common-refs", "SKILL.md"),
		"---\nname: common-refs\ndescription: Uses common filenames\n---\n"+
			"See `CHANGELOG.md` and `README.md` and `LICENSE.md` and `CLAUDE.md`\n")
	findings := ValidateAll(root)
	assertNoFinding(t, findings, "PD12")
}

func TestPD12_DoubleBacktickIgnored(t *testing.T) {
	root := makeGoodFixture(t)
	// Double-backtick code spans should not trigger PD12.
	writeFile(t, filepath.Join(root, "skills", "double-bt", "SKILL.md"),
		"---\nname: double-bt\ndescription: Has double backtick\n---\n"+
			"Example: ``REFERENCE.md`` is code\n")
	findings := ValidateAll(root)
	// The double-backtick reference should be ignored.
	assertNoFinding(t, findings, "PD12")
}

// ---------------------------------------------------------------------------
// PD13 — skill agent references valid
// ---------------------------------------------------------------------------

func TestPD13_AgentNotFound(t *testing.T) {
	root := makeGoodFixture(t)
	writeFile(t, filepath.Join(root, "skills", "has-agent-ref", "SKILL.md"),
		"---\nname: has-agent-ref\ndescription: References an agent\n---\nUse agents/nonexistent-agent for help\n")
	findings := ValidateAll(root)
	assertHasFinding(t, findings, "PD13", "error")
	assertNoFinding(t, findings, "PD11")
	assertNoFinding(t, findings, "PD12")
}

func TestPD13_AgentExists(t *testing.T) {
	root := makeGoodFixture(t)
	writeFile(t, filepath.Join(root, "skills", "has-agent-ref", "SKILL.md"),
		"---\nname: has-agent-ref\ndescription: References an agent\n---\nUse agents/my-agent for help\n")
	// agents/my-agent.md already exists in the good fixture.
	findings := ValidateAll(root)
	assertNoFinding(t, findings, "PD13")
}

// ---------------------------------------------------------------------------
// PD14 — skill script references valid (warning)
// ---------------------------------------------------------------------------

func TestPD14_ScriptNotFound(t *testing.T) {
	root := makeGoodFixture(t)
	writeFile(t, filepath.Join(root, "skills", "has-script-ref", "SKILL.md"),
		"---\nname: has-script-ref\ndescription: References a script\n---\n"+
			"Run find . -name 'missing-lib.js' to find it\n")
	findings := ValidateAll(root)
	assertHasFinding(t, findings, "PD14", "warning")
	assertNoFinding(t, findings, "PD11")
	assertNoFinding(t, findings, "PD12")
	assertNoFinding(t, findings, "PD13")
}

// ---------------------------------------------------------------------------
// PD15 — hooks.json valid JSON
// ---------------------------------------------------------------------------

func TestPD15_HooksJSONMissing(t *testing.T) {
	root := makeGoodFixture(t)
	if err := os.Remove(filepath.Join(root, "hooks", "hooks.json")); err != nil {
		t.Fatal(err)
	}
	findings := ValidateAll(root)
	assertHasFinding(t, findings, "PD15", "error")
	assertNoFinding(t, findings, "PD1")
}

func TestPD15_HooksJSONInvalid(t *testing.T) {
	root := makeGoodFixture(t)
	writeFile(t, filepath.Join(root, "hooks", "hooks.json"), "broken json {")
	findings := ValidateAll(root)
	assertHasFinding(t, findings, "PD15", "error")
}

// ---------------------------------------------------------------------------
// PD16 — agents discoverable (frontmatter with name+description+tools, warning)
// ---------------------------------------------------------------------------

func TestPD16_MissingFrontmatter(t *testing.T) {
	root := makeGoodFixture(t)
	writeFile(t, filepath.Join(root, "agents", "bad-agent.md"),
		"No frontmatter\n")
	findings := ValidateAll(root)
	assertHasFinding(t, findings, "PD16", "warning")
}

func TestPD16_MissingTools(t *testing.T) {
	root := makeGoodFixture(t)
	writeFile(t, filepath.Join(root, "agents", "no-tools.md"),
		"---\nname: no-tools\ndescription: Agent without tools\n---\nContent\n")
	findings := ValidateAll(root)
	assertHasFinding(t, findings, "PD16", "warning")
	// Should be specifically about missing "tools".
	pd16 := findByID(findings, "PD16")
	found := false
	for _, f := range pd16 {
		if f.Message == "test-plugin/agents/no-tools.md: frontmatter missing \"tools\"" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected PD16 finding about missing tools, got %v", pd16)
	}
}

// ---------------------------------------------------------------------------
// Severity verification
// ---------------------------------------------------------------------------

func TestSeverity_WarningChecks(t *testing.T) {
	// PD2 is the only marketplace-level warning.
	root := makeGoodFixture(t)
	writeJSON(t, filepath.Join(root, ".claude-plugin", "marketplace.json"), map[string]any{
		"name": "test-project",
		"plugins": []map[string]any{
			{"name": "test-plugin", "source": "."},
		},
	})
	findings := ValidateAll(root)
	for _, f := range findByID(findings, "PD2") {
		if f.Severity != "warning" {
			t.Errorf("PD2 severity should be 'warning', got %q", f.Severity)
		}
	}
}

// ---------------------------------------------------------------------------
// Extractor unit tests
// ---------------------------------------------------------------------------

func TestExtractSkillRefs(t *testing.T) {
	content := "First Invoke the `plan-sdlc` skill and then Invoke the `review-sdlc` skill."
	refs := extractSkillRefs(content)
	if len(refs) != 2 {
		t.Fatalf("expected 2 skill refs, got %d: %v", len(refs), refs)
	}
	if refs[0] != "plan-sdlc" || refs[1] != "review-sdlc" {
		t.Errorf("unexpected skill refs: %v", refs)
	}
}

func TestExtractAgentRefs(t *testing.T) {
	content := "Use agents/commit-orchestrator and the `review-orchestrator` agent."
	refs := extractAgentRefs(content)
	if len(refs) != 2 {
		t.Fatalf("expected 2 agent refs, got %d: %v", len(refs), refs)
	}
	// Order: agents/ path pattern first, then backtick pattern.
	if refs[0] != "commit-orchestrator" || refs[1] != "review-orchestrator" {
		t.Errorf("unexpected agent refs: %v", refs)
	}
}

func TestExtractScriptRefs(t *testing.T) {
	content := `Run find . -name 'commit.js' to locate it.
Also check -path "*/sdlc*/scripts/skill/review.js" for the review.
And plugins/sdlc-utilities/scripts/lib/config.js for config.`
	refs := extractScriptRefs(content)
	// -path and direct patterns take priority; -name falls back.
	if len(refs) != 3 {
		t.Fatalf("expected 3 script refs, got %d: %v", len(refs), refs)
	}
}

func TestExtractSiblingFileRefs(t *testing.T) {
	content := "See `REFERENCE.md` and `EXAMPLES.md`. Also ``IGNORED.md`` is code."
	refs := extractSiblingFileRefs(content)
	if len(refs) != 2 {
		t.Fatalf("expected 2 sibling refs, got %d: %v", len(refs), refs)
	}
}

func TestExtractSiblingFileRefs_NonSiblings(t *testing.T) {
	content := "`CHANGELOG.md` and `README.md` and `LICENSE.md` and `CLAUDE.md` and `SKILL.md`"
	refs := extractSiblingFileRefs(content)
	if len(refs) != 0 {
		t.Errorf("expected 0 sibling refs (all non-sibling), got %d: %v", len(refs), refs)
	}
}

// ---------------------------------------------------------------------------
// JS truthiness helper
// ---------------------------------------------------------------------------

func TestJsTruthy(t *testing.T) {
	tests := []struct {
		input any
		want  bool
	}{
		{nil, false},
		{"", false},
		{"hello", true},
		{0, true},       // diverges from JS (0 is falsy), but irrelevant for our use cases
		{[]any{}, true}, // matches JS (empty array is truthy)
		{true, true},
		{false, true}, // diverges from JS (false is falsy), but irrelevant for our use cases
	}
	for _, tt := range tests {
		got := jsTruthy(tt.input)
		if got != tt.want {
			t.Errorf("jsTruthy(%v) = %v, want %v", tt.input, got, tt.want)
		}
	}
}
