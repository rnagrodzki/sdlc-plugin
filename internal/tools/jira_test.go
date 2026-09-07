package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// jiraTestRoot returns a fresh mainRoot with no .sdlc/config.json and no
// legacy markers, so configmigrate.Verify(mainRoot) passes (v5-clean).
func jiraTestRoot(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

func writeJSONFile(t *testing.T, path string, v any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// ---------------------------------------------------------------------------
// KD5 config-version gate
// ---------------------------------------------------------------------------

func TestJiraConfigVersionGate(t *testing.T) {
	root := jiraTestRoot(t)
	if err := os.MkdirAll(filepath.Join(root, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".claude", "sdlc.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := jiraCore(root, JiraIn{Action: "check", Key: "FOO"}, true)
	if err != nil {
		t.Fatalf("expected soft nil-error payload, got err: %v", err)
	}
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any payload, got %T", out)
	}
	errs, ok := m["errors"].([]string)
	if !ok || len(errs) == 0 {
		t.Fatalf("expected non-empty errors slice, got %#v", m["errors"])
	}

	// SkipConfigCheck bypasses the gate entirely.
	out2, err2 := jiraCore(root, JiraIn{Action: "check", Key: "FOO", CacheDir: t.TempDir(), SkipConfigCheck: true}, true)
	if err2 != nil {
		t.Fatalf("unexpected error with SkipConfigCheck: %v", err2)
	}
	m2, ok := out2.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any payload, got %T", out2)
	}
	if _, hasErrorsOnly := m2["exists"]; !hasErrorsOnly {
		t.Fatalf("expected full check payload when config check skipped, got %#v", m2)
	}
}

// ---------------------------------------------------------------------------
// Key-required validation
// ---------------------------------------------------------------------------

func TestJiraKeyRequiredExceptValidateBody(t *testing.T) {
	root := jiraTestRoot(t)

	if _, err := jiraCore(root, JiraIn{Action: "check"}, true); err == nil {
		t.Fatal("expected error for missing key on check")
	}
	if _, err := jiraCore(root, JiraIn{Action: "copy-template", TemplateType: "Bug", TemplateFrom: "Task", TemplatesDir: t.TempDir()}, true); err == nil {
		t.Fatal("expected error for missing key on copy-template (JS parity quirk)")
	}
	// validate-body must NOT require key.
	if _, err := jiraCore(root, JiraIn{Action: "validate-body", MarkdownBody: "no urls here"}, true); err != nil {
		t.Fatalf("validate-body should not require key: %v", err)
	}
}

func TestJiraUnknownAction(t *testing.T) {
	root := jiraTestRoot(t)
	if _, err := jiraCore(root, JiraIn{Action: "bogus", Key: "FOO"}, true); err == nil {
		t.Fatal("expected error for unknown action")
	}
}

// ---------------------------------------------------------------------------
// check
// ---------------------------------------------------------------------------

func TestJiraCheckNoCache(t *testing.T) {
	root := jiraTestRoot(t)
	cacheDir := t.TempDir()

	out, err := jiraCore(root, JiraIn{Action: "check", Key: "foo", CacheDir: cacheDir}, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m := out.(map[string]any)
	if m["exists"] != false {
		t.Fatalf("expected exists=false, got %#v", m["exists"])
	}
	if m["projectKey"] != "FOO" {
		t.Fatalf("expected key uppercased to FOO, got %#v", m["projectKey"])
	}
	missing, _ := m["missing"].([]string)
	if len(missing) != 1 || missing[0] != "all" {
		t.Fatalf("expected missing=[all], got %#v", m["missing"])
	}
}

func TestJiraCheckPopulatedFresh(t *testing.T) {
	root := jiraTestRoot(t)
	cacheDir := t.TempDir()

	cache := map[string]any{
		"version":     1,
		"cloudId":     "cloud-1",
		"siteUrl":     "https://example.atlassian.net",
		"currentUser": map[string]any{"accountId": "u1"},
		"project":     map[string]any{"key": "FOO"},
		"issueTypes": map[string]any{
			"Task": map[string]any{"id": "1"},
			"Bug":  map[string]any{"id": "2"},
		},
		"fieldSchemas": map[string]any{
			"Task": map[string]any{}, "Bug": map[string]any{},
		},
		"workflows": map[string]any{
			"Task": map[string]any{}, "Bug": map[string]any{},
		},
		"linkTypes":    []any{"blocks"},
		"userMappings": map[string]any{"alice": "u1"},
		"lastUpdated":  time.Now().Format(time.RFC3339),
		"maxAgeHours":  float64(24),
	}
	writeJSONFile(t, filepath.Join(cacheDir, "FOO.json"), cache)

	// TemplatesDir is set explicitly (rather than left empty) so this test
	// never falls through to jiraResolveTemplatesDir's real
	// ~/.claude/plugins directory walk — keeping the check action fully
	// hermetic and independent of what plugins happen to be installed on
	// the machine running the test.
	out, err := jiraCore(root, JiraIn{Action: "check", Key: "FOO", CacheDir: cacheDir, TemplatesDir: t.TempDir()}, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m := out.(map[string]any)
	if m["exists"] != true {
		t.Fatalf("expected exists=true, got %#v", m["exists"])
	}
	if m["fresh"] != true {
		t.Fatalf("expected fresh=true, got %#v", m["fresh"])
	}
	missing, _ := m["missing"].([]string)
	if len(missing) != 0 {
		t.Fatalf("expected no missing sections, got %#v", missing)
	}
}

func TestJiraCheckStale(t *testing.T) {
	root := jiraTestRoot(t)
	cacheDir := t.TempDir()

	cache := map[string]any{
		"cloudId":     "cloud-1",
		"lastUpdated": time.Now().Add(-48 * time.Hour).Format(time.RFC3339),
		"maxAgeHours": float64(24),
	}
	writeJSONFile(t, filepath.Join(cacheDir, "FOO.json"), cache)

	out, err := jiraCore(root, JiraIn{Action: "check", Key: "FOO", CacheDir: cacheDir, TemplatesDir: t.TempDir()}, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m := out.(map[string]any)
	if m["exists"] != true {
		t.Fatalf("expected exists=true, got %#v", m["exists"])
	}
	if m["fresh"] != false {
		t.Fatalf("expected fresh=false for stale cache, got %#v", m["fresh"])
	}
}

func TestJiraCheckProjectMembershipViolation(t *testing.T) {
	root := jiraTestRoot(t)
	writeJSONFile(t, filepath.Join(root, paths.DataDir, "config.json"), map[string]any{
		"jira": map[string]any{"projects": []string{"FOO", "BAR"}},
	})

	_, err := jiraCore(root, JiraIn{Action: "check", Key: "BAZ", CacheDir: t.TempDir()}, true)
	if err == nil {
		t.Fatal("expected DomainError for project not in jira.projects")
	}
}

// ---------------------------------------------------------------------------
// save / load
// ---------------------------------------------------------------------------

func TestJiraSaveRequiresFields(t *testing.T) {
	root := jiraTestRoot(t)
	_, err := jiraCore(root, JiraIn{
		Action:   "save",
		Key:      "FOO",
		CacheDir: t.TempDir(),
		Data:     map[string]any{"version": float64(1)},
	}, true)
	if err == nil {
		t.Fatal("expected error for missing required fields")
	}
}

func TestJiraSaveThenLoadRoundTrip(t *testing.T) {
	root := jiraTestRoot(t)
	cacheDir := t.TempDir()
	data := map[string]any{
		"version": float64(1),
		"cloudId": "cloud-1",
		"project": map[string]any{"key": "FOO"},
		"siteUrl": "https://example.atlassian.net",
	}

	saveOut, err := jiraCore(root, JiraIn{Action: "save", Key: "FOO", CacheDir: cacheDir, Data: data}, true)
	if err != nil {
		t.Fatalf("save failed: %v", err)
	}
	sm := saveOut.(map[string]any)
	if sm["saved"] != true {
		t.Fatalf("expected saved=true, got %#v", sm)
	}

	loadOut, err := jiraCore(root, JiraIn{Action: "load", Key: "FOO", CacheDir: cacheDir}, true)
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}
	lm := loadOut.(map[string]any)
	if lm["cloudId"] != "cloud-1" {
		t.Fatalf("expected round-tripped cloudId, got %#v", lm["cloudId"])
	}
}

func TestJiraLoadMissingCache(t *testing.T) {
	root := jiraTestRoot(t)
	_, err := jiraCore(root, JiraIn{Action: "load", Key: "NOPE", CacheDir: t.TempDir()}, true)
	if err == nil {
		t.Fatal("expected error loading nonexistent cache")
	}
}

// ---------------------------------------------------------------------------
// save-field
// ---------------------------------------------------------------------------

func TestJiraSaveFieldRequiresData(t *testing.T) {
	root := jiraTestRoot(t)
	cacheDir := t.TempDir()
	writeJSONFile(t, filepath.Join(cacheDir, "FOO.json"), map[string]any{"cloudId": "c1"})

	_, err := jiraCore(root, JiraIn{Action: "save-field", Key: "FOO", CacheDir: cacheDir, FieldName: "issueTypes"}, true)
	if err == nil {
		t.Fatal("expected error when data is nil for save-field")
	}
}

func TestJiraSaveFieldMergesPlainObjects(t *testing.T) {
	root := jiraTestRoot(t)
	cacheDir := t.TempDir()
	writeJSONFile(t, filepath.Join(cacheDir, "FOO.json"), map[string]any{
		"issueTypes": map[string]any{"Task": map[string]any{"id": "1"}},
	})

	out, err := jiraCore(root, JiraIn{
		Action: "save-field", Key: "FOO", CacheDir: cacheDir,
		FieldName: "issueTypes",
		Data:      map[string]any{"Bug": map[string]any{"id": "2"}},
	}, true)
	if err != nil {
		t.Fatalf("save-field failed: %v", err)
	}
	_ = out

	var cache map[string]any
	b, err := os.ReadFile(filepath.Join(cacheDir, "FOO.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &cache); err != nil {
		t.Fatal(err)
	}
	issueTypes := cache["issueTypes"].(map[string]any)
	if _, ok := issueTypes["Task"]; !ok {
		t.Fatalf("expected merge to preserve existing Task field, got %#v", issueTypes)
	}
	if _, ok := issueTypes["Bug"]; !ok {
		t.Fatalf("expected merge to add new Bug field, got %#v", issueTypes)
	}
}

func TestJiraSaveFieldOverwritesNonObject(t *testing.T) {
	root := jiraTestRoot(t)
	cacheDir := t.TempDir()
	writeJSONFile(t, filepath.Join(cacheDir, "FOO.json"), map[string]any{
		"linkTypes": []any{"blocks"},
	})

	_, err := jiraCore(root, JiraIn{
		Action: "save-field", Key: "FOO", CacheDir: cacheDir,
		FieldName: "linkTypes",
		Data:      map[string]any{"replaced": true},
	}, true)
	if err != nil {
		t.Fatalf("save-field failed: %v", err)
	}

	var cache map[string]any
	b, err := os.ReadFile(filepath.Join(cacheDir, "FOO.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &cache); err != nil {
		t.Fatal(err)
	}
	lt, ok := cache["linkTypes"].(map[string]any)
	if !ok {
		t.Fatalf("expected linkTypes overwritten with object, got %#v", cache["linkTypes"])
	}
	if lt["replaced"] != true {
		t.Fatalf("expected overwritten value, got %#v", lt)
	}
}

// ---------------------------------------------------------------------------
// templates / init-templates / copy-template
// ---------------------------------------------------------------------------

func TestJiraTemplatesResolution(t *testing.T) {
	root := jiraTestRoot(t)
	cacheDir := t.TempDir()
	templatesDir := t.TempDir()

	writeJSONFile(t, filepath.Join(cacheDir, "FOO.json"), map[string]any{
		"issueTypes": map[string]any{
			"Task":     map[string]any{},
			"Sub-task": map[string]any{}, // no default Sub-task.md, falls back to Task
			"Epic":     map[string]any{}, // no default and no fallback -> none
		},
	})
	if err := os.WriteFile(filepath.Join(templatesDir, "Task.md"), []byte("# Task"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Pre-seed a custom template for Epic to exercise the "custom" branch.
	if err := os.MkdirAll(filepath.Join(root, paths.DataDir, "jira-templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, paths.DataDir, "jira-templates", "Epic.md"), []byte("custom"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := jiraCore(root, JiraIn{Action: "templates", Key: "FOO", CacheDir: cacheDir, TemplatesDir: templatesDir}, true)
	if err != nil {
		t.Fatalf("templates failed: %v", err)
	}
	m := out.(map[string]any)
	resolved := m["resolved"].(map[string]any)

	if resolved["Task"] != "default" {
		t.Errorf("expected Task=default, got %#v", resolved["Task"])
	}
	if resolved["Sub-task"] != "default-fallback" {
		t.Errorf("expected Sub-task=default-fallback, got %#v", resolved["Sub-task"])
	}
	if resolved["Epic"] != "custom" {
		t.Errorf("expected Epic=custom, got %#v", resolved["Epic"])
	}
}

func TestJiraInitTemplates(t *testing.T) {
	root := jiraTestRoot(t)
	cacheDir := t.TempDir()
	templatesDir := t.TempDir()

	writeJSONFile(t, filepath.Join(cacheDir, "FOO.json"), map[string]any{
		"issueTypes": map[string]any{
			"Task": map[string]any{},
			"Epic": map[string]any{}, // no default -> unavailable
		},
	})
	if err := os.WriteFile(filepath.Join(templatesDir, "Task.md"), []byte("# Task"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := jiraCore(root, JiraIn{Action: "init-templates", Key: "FOO", CacheDir: cacheDir, TemplatesDir: templatesDir}, true)
	if err != nil {
		t.Fatalf("init-templates failed: %v", err)
	}
	m := out.(map[string]any)

	initialized, _ := m["initialized"].([]string)
	unavailable, _ := m["unavailable"].([]string)
	if len(initialized) != 1 || initialized[0] != "Task" {
		t.Fatalf("expected initialized=[Task], got %#v", initialized)
	}
	if len(unavailable) != 1 || unavailable[0] != "Epic" {
		t.Fatalf("expected unavailable=[Epic], got %#v", unavailable)
	}

	dst := filepath.Join(root, paths.DataDir, "jira-templates", "Task.md")
	if !fileExists(dst) {
		t.Fatalf("expected copied template at %s", dst)
	}

	// Second run should skip the now-existing Task.md.
	out2, err := jiraCore(root, JiraIn{Action: "init-templates", Key: "FOO", CacheDir: cacheDir, TemplatesDir: templatesDir}, true)
	if err != nil {
		t.Fatalf("second init-templates failed: %v", err)
	}
	m2 := out2.(map[string]any)
	skipped, _ := m2["skipped"].([]string)
	if len(skipped) != 1 || skipped[0] != "Task" {
		t.Fatalf("expected skipped=[Task] on rerun, got %#v", skipped)
	}
}

func TestJiraCopyTemplate(t *testing.T) {
	root := jiraTestRoot(t)
	templatesDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(templatesDir, "Task.md"), []byte("# Task"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := jiraCore(root, JiraIn{
		Action: "copy-template", Key: "FOO", TemplatesDir: templatesDir,
		TemplateType: "Sub-bug", TemplateFrom: "Task",
	}, true)
	if err != nil {
		t.Fatalf("copy-template failed: %v", err)
	}
	m := out.(map[string]any)
	if m["copied"] != true {
		t.Fatalf("expected copied=true, got %#v", m)
	}

	dst := filepath.Join(root, paths.DataDir, "jira-templates", "Sub-bug.md")
	if !fileExists(dst) {
		t.Fatalf("expected copied file at %s", dst)
	}

	// Second copy should report exists, not overwrite.
	out2, err := jiraCore(root, JiraIn{
		Action: "copy-template", Key: "FOO", TemplatesDir: templatesDir,
		TemplateType: "Sub-bug", TemplateFrom: "Task",
	}, true)
	if err != nil {
		t.Fatalf("second copy-template failed: %v", err)
	}
	m2 := out2.(map[string]any)
	if m2["copied"] != false || m2["reason"] != "exists" {
		t.Fatalf("expected copied=false reason=exists, got %#v", m2)
	}
}

func TestJiraCopyTemplateMissingSource(t *testing.T) {
	root := jiraTestRoot(t)
	templatesDir := t.TempDir()
	_, err := jiraCore(root, JiraIn{
		Action: "copy-template", Key: "FOO", TemplatesDir: templatesDir,
		TemplateType: "Bug", TemplateFrom: "DoesNotExist",
	}, true)
	if err == nil {
		t.Fatal("expected error for missing template source")
	}
}

// ---------------------------------------------------------------------------
// clear
// ---------------------------------------------------------------------------

func TestJiraClear(t *testing.T) {
	root := jiraTestRoot(t)
	cacheDir := t.TempDir()
	writeJSONFile(t, filepath.Join(cacheDir, "FOO.json"), map[string]any{"cloudId": "c1"})

	out, err := jiraCore(root, JiraIn{Action: "clear", Key: "FOO", CacheDir: cacheDir}, true)
	if err != nil {
		t.Fatalf("clear failed: %v", err)
	}
	m := out.(map[string]any)
	if m["cleared"] != true {
		t.Fatalf("expected cleared=true, got %#v", m)
	}
	if fileExists(filepath.Join(cacheDir, "FOO.json")) {
		t.Fatal("expected cache file removed")
	}
}

// ---------------------------------------------------------------------------
// validate-body
// ---------------------------------------------------------------------------

func TestJiraValidateBodyOfflineGenericURLSkipped(t *testing.T) {
	root := jiraTestRoot(t)
	body := "See https://example.com/docs for details."

	out, err := jiraCore(root, JiraIn{Action: "validate-body", MarkdownBody: body}, true)
	if err != nil {
		t.Fatalf("validate-body failed: %v", err)
	}
	res := out.(JiraValidateBodyOut)
	if !res.OK {
		t.Fatalf("expected ok=true (offline generic URL is skipped, not a violation), got %#v", res)
	}
	if len(res.Skipped) != 1 || res.Skipped[0].Reason != "offline" {
		t.Fatalf("expected one skipped url with reason=offline, got %#v", res.Skipped)
	}
	if res.ADF == nil {
		t.Fatal("expected ADF conversion present when MarkdownBody is non-empty")
	}
}

func TestJiraValidateBodyNoMarkdownBodyOmitsADF(t *testing.T) {
	root := jiraTestRoot(t)
	out, err := jiraCore(root, JiraIn{Action: "validate-body"}, true)
	if err != nil {
		t.Fatalf("validate-body failed: %v", err)
	}
	res := out.(JiraValidateBodyOut)
	if res.ADF != nil {
		t.Fatalf("expected no ADF when MarkdownBody is empty, got %#v", res.ADF)
	}
	if !res.OK {
		t.Fatalf("expected ok=true for empty body, got %#v", res)
	}
}

func TestJiraValidateBodyAtlassianSiteMatch(t *testing.T) {
	root := jiraTestRoot(t)
	siteCacheDir := t.TempDir()
	// Site-sharded layout mirrors the real ~/.sdlc-cache/jira/<site>/ shape
	// that internal/links.discoverJiraSiteFromCache scans for. jira.go's
	// own cache lookups are irrelevant to validate-body (no Key is used);
	// this directory exists solely to be passed through as
	// links.Ctx.JiraCacheDir.
	if err := os.MkdirAll(filepath.Join(siteCacheDir, "example_atlassian_net"), 0o755); err != nil {
		t.Fatal(err)
	}

	body := "Ticket: https://example.atlassian.net/browse/FOO-1"
	out, err := jiraCore(root, JiraIn{Action: "validate-body", MarkdownBody: body, CacheDir: siteCacheDir}, true)
	if err != nil {
		t.Fatalf("validate-body failed: %v", err)
	}
	res := out.(JiraValidateBodyOut)
	if !res.OK {
		t.Fatalf("expected ok=true for matching cached site, got %#v", res)
	}
}

func TestJiraValidateBodyAtlassianSiteMismatch(t *testing.T) {
	root := jiraTestRoot(t)
	siteCacheDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(siteCacheDir, "other_atlassian_net"), 0o755); err != nil {
		t.Fatal(err)
	}

	body := "Ticket: https://example.atlassian.net/browse/FOO-1"
	out, err := jiraCore(root, JiraIn{Action: "validate-body", MarkdownBody: body, CacheDir: siteCacheDir}, true)
	if err != nil {
		t.Fatalf("validate-body failed: %v", err)
	}
	res := out.(JiraValidateBodyOut)
	if res.OK {
		t.Fatal("expected ok=false for site mismatch")
	}
	if len(res.Violations) != 1 || res.Violations[0].Reason != "atlassian-site-mismatch" {
		t.Fatalf("expected one atlassian-site-mismatch violation, got %#v", res.Violations)
	}
	if res.Message == "" {
		t.Fatal("expected non-empty formatted violation message")
	}
}

func TestJiraValidateBodyAtlassianAmbiguousSites(t *testing.T) {
	root := jiraTestRoot(t)
	siteCacheDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(siteCacheDir, "site_a_atlassian_net"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(siteCacheDir, "site_b_atlassian_net"), 0o755); err != nil {
		t.Fatal(err)
	}

	body := "Ticket: https://example.atlassian.net/browse/FOO-1"
	out, err := jiraCore(root, JiraIn{Action: "validate-body", MarkdownBody: body, CacheDir: siteCacheDir}, true)
	if err != nil {
		t.Fatalf("validate-body failed: %v", err)
	}
	res := out.(JiraValidateBodyOut)
	if res.OK {
		t.Fatal("expected ok=false for ambiguous multi-site cache")
	}
	if len(res.Violations) != 1 || res.Violations[0].Reason != "atlassian-site-ambiguous" {
		t.Fatalf("expected one atlassian-site-ambiguous violation (disclosed multi-site limitation), got %#v", res.Violations)
	}
}

func TestJiraValidateBodyLineNumberTracking(t *testing.T) {
	root := jiraTestRoot(t)
	body := "line one\nsee https://example.com/a\nline three\nand https://example.com/b"

	out, err := jiraCore(root, JiraIn{Action: "validate-body", MarkdownBody: body}, true)
	if err != nil {
		t.Fatalf("validate-body failed: %v", err)
	}
	res := out.(JiraValidateBodyOut)
	if len(res.Skipped) != 2 {
		t.Fatalf("expected 2 skipped urls, got %#v", res.Skipped)
	}
	byURL := map[string]int{}
	for _, s := range res.Skipped {
		byURL[s.URL] = s.Line
	}
	if byURL["https://example.com/a"] != 2 {
		t.Errorf("expected line 2 for first url, got %d", byURL["https://example.com/a"])
	}
	if byURL["https://example.com/b"] != 4 {
		t.Errorf("expected line 4 for second url, got %d", byURL["https://example.com/b"])
	}
}

func TestJiraExtractUrlsDedupesAndTrimsPunctuation(t *testing.T) {
	urls := jiraExtractUrls("See https://example.com/a, and also https://example.com/a. Also (https://example.com/b).")
	if len(urls) != 2 {
		t.Fatalf("expected 2 deduped urls, got %#v", urls)
	}
	if urls[0].URL != "https://example.com/a" {
		t.Errorf("expected trailing comma stripped, got %q", urls[0].URL)
	}
	if urls[1].URL != "https://example.com/b" {
		t.Errorf("expected trailing paren stripped, got %q", urls[1].URL)
	}
}

// ---------------------------------------------------------------------------
// RegisterJiraTools smoke test
// ---------------------------------------------------------------------------

func TestJiraRegisterJiraToolsDoesNotPanic(t *testing.T) {
	s := mcpserver.New("test", "0.0.0")
	RegisterJiraTools(s)
}
