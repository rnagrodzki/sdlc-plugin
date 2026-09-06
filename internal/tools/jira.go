package tools

// Package-level: jira tool.
//
// This is a Go port of scripts/skill/jira.js (cache and template management
// for the jira-sdlc skill). jira.js is pure local cache/template management —
// it has no HTTP/auth code of its own; the only network-touching surface is
// validate-body's URL reachability check, which is delegated entirely to
// internal/links (Task 12).
//
// Consolidated per KD7 into a single "jira" tool with a 9-value Action enum,
// mirroring jira.js's --check/--load/--save/--save-field/--templates/
// --init-templates/--clear/--copy-template/--validate-body subcommands.
//
// Disclosed deviations from jira.js (all judgment calls made during this
// port; see the task-28 fact sheet for the rulings that constrain them):
//
//  1. Legacy cache auto-migration (findLegacyCache/migrateLegacyCache for
//     .sdlc/jira-cache and .claude/jira-cache) is DROPPED. This follows the
//     KD2 clean-break precedent already established for config (Task 7) and
//     other legacy-migration surfaces (Task 21): pre-v5 on-disk layouts are
//     not auto-migrated by the Go port. resolveEffectiveCachePath simply
//     returns no match instead of probing/migrating legacy locations.
//  2. runAutoMigration()'s templates-dir migration (.claude/jira-templates/
//     -> .sdlc/jira-templates/, delegated in JS to an unported
//     migrate-jira-templates.js) is likewise DROPPED for the same reason —
//     it depends on a script outside this task's file scope, and templates
//     written under the legacy .claude/ location are simply not seen; a
//     fresh .sdlc/jira-templates/ tree is used from a clean slate.
//  3. Two additive JiraIn fields not in the literal task-28 contract list:
//     - Data map[string]any: save/save-field read arbitrary JSON from
//       stdin in jira.js; an MCP tool has no stdin, so the payload must
//       travel as a typed field. This is the direct structural analogue.
//     - TemplatesDir string: mirrors jira.js's --templates-dir override.
//       Needed so tests can exercise templates/init-templates/check/
//       copy-template without depending on (or mutating) a real
//       ~/.claude/plugins installation on the machine running the tests.
//  4. flags.skipWorkflowDiscovery (JS's checkCache echoes --skip-workflow-
//     discovery back in its output, but never reads it for behavior — it is
//     dead even in jira.js) is dropped rather than added as a tenth input
//     field; check's flags.skipWorkflowDiscovery is hardcoded false.
//  5. save-field additionally requires Data to be non-nil (JS can only reach
//     its merge logic with a successfully-parsed stdin JSON value; a typed
//     caller that omits Data entirely most likely made a mistake, so this is
//     rejected rather than silently writing a null field).
//  6. Site is fully honored for cache-path disambiguation across all
//     non-validate-body actions (mirrors JS's ctx.site exactly — this has
//     nothing to do with internal/links' Ctx shape). For validate-body
//     specifically, per the task-28 ruling, internal/links.Ctx has no
//     jiraSite-override field, so Site (and the cache-lookup-based jiraSite
//     resolution jira.js performs before calling validateLinks) has no
//     effect on the link-check outcome there; only auto-discovery via
//     JiraCacheDir applies. This is a disclosed parity gap, not a bug.
//  7. Offline (for the validate-body -> internal/links.Validate call) is
//     resolved by the Register wiring from the SDLC_LINKS_OFFLINE=1
//     environment variable, matching jira.js's reliance on that same env var
//     inside validateLinks. Per Task 12's decisions, internal/links itself
//     does not read the env var, so the caller (this file) does instead. The
//     testable core entry point takes offline as an explicit bool so tests
//     can force it without touching the environment.
//  8. Cache freshness (check action) only recognizes RFC3339/RFC3339Nano
//     lastUpdated strings, whereas JS's `new Date(x)` also accepts epoch-
//     millis numbers and a broader set of string formats. No current writer
//     produces those forms; this is a narrow, disclosed gap rather than a
//     hand-rolled JS-Date-compatible parser.
//  9. Object key ordering (e.g. issueTypes derived from Object.keys on a
//     cached issueTypes object) is not preserved — Go maps have no stable
//     iteration order. Where jira.js's output order depends on it, this port
//     sorts alphabetically for determinism. Callers should treat these as
//     sets, not ordered lists.

import (
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/adf"
	"github.com/rnagrodzki/sdlc-plugin/internal/config"
	"github.com/rnagrodzki/sdlc-plugin/internal/configmigrate"
	"github.com/rnagrodzki/sdlc-plugin/internal/fsx"
	"github.com/rnagrodzki/sdlc-plugin/internal/links"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/worktree"
)

// ---------------------------------------------------------------------------
// Contract
// ---------------------------------------------------------------------------

// JiraIn is the input contract for the jira tool. Action selects one of the
// 9 jira.js subcommands; the remaining fields are a superset of what each
// action needs (unused fields for a given action are ignored).
type JiraIn struct {
	Action string `json:"action"`

	// Key is the Jira project key (jira.js's --project). Uppercased on use.
	// Required for every action except validate-body (mirrors jira.js's
	// parseArgs, which enforces --project for every subcommand but
	// validate-body — including copy-template, which does not actually use
	// it; that quirk is preserved for fidelity).
	Key string `json:"key,omitempty"`

	// MarkdownBody is the Jira description/comment body for validate-body.
	MarkdownBody string `json:"markdownBody,omitempty"`

	// CacheDir overrides the default ~/.sdlc-cache/jira/<site>/ layout with
	// a flat <CacheDir>/<KEY>.json path (jira.js's --cache-dir). This is
	// also the hermetic-test hook: point it at a temp dir to avoid touching
	// the real home cache.
	CacheDir string `json:"cacheDir,omitempty"`

	// FieldName is the cache field to merge/overwrite for save-field.
	FieldName string `json:"fieldName,omitempty"`

	// TemplateType is the destination issue-type name for copy-template.
	TemplateType string `json:"templateType,omitempty"`

	// TemplateFrom is the source template name (without .md) for
	// copy-template.
	TemplateFrom string `json:"templateFrom,omitempty"`

	// Site optionally disambiguates the home cache by site host, and (for
	// validate-body only, per the disclosed limitation above) is otherwise
	// inert.
	Site string `json:"site,omitempty"`

	// TemplatesDir overrides plugin-tree discovery of the shipped jira-sdlc
	// templates/ directory (jira.js's --templates-dir). See deviation #3.
	TemplatesDir string `json:"templatesDir,omitempty"`

	// Data carries the JSON payload for save/save-field (jira.js reads this
	// from stdin). See deviation #3.
	Data map[string]any `json:"data,omitempty"`

	SkipConfigCheck bool `json:"skipConfigCheck,omitempty"`
}

// JiraValidateBodyOut is the validate-body action's payload.
type JiraValidateBodyOut struct {
	OK         bool            `json:"ok"`
	Violations []jiraViolation `json:"violations"`
	Skipped    []jiraSkipped   `json:"skipped"`
	// Message is jira.js's formatViolations() output, included only when
	// !OK (jira.js only ever renders this string on the failure path).
	Message string `json:"message,omitempty"`
	// ADF is the markdown-to-ADF conversion of MarkdownBody (internal/adf,
	// Task 18), present only when MarkdownBody was non-empty.
	ADF map[string]any `json:"adf,omitempty"`
}

type jiraViolation struct {
	URL    string `json:"url"`
	Line   int    `json:"line,omitempty"`
	Reason string `json:"reason"`
	Detail string `json:"detail,omitempty"`
}

type jiraSkipped struct {
	URL    string `json:"url"`
	Line   int    `json:"line,omitempty"`
	Reason string `json:"reason"`
}

// ---------------------------------------------------------------------------
// Home-cache path helpers (mirrors jira.js's getHomeCacheRoot / sanitizeSiteHost
// / resolveCandidatePaths / getCachePath)
// ---------------------------------------------------------------------------

func jiraHomeCacheRoot() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", ".sdlc-cache", "jira")
	}
	return filepath.Join(home, ".sdlc-cache", "jira")
}

var jiraSchemeRe = regexp.MustCompile(`(?i)^https?://`)

// jiraSanitizeSiteHost ports jira.js's sanitizeSiteHost: derive a
// filesystem-safe site-host directory name ("example.atlassian.net" ->
// "example_atlassian_net") from a site URL, or a bare host.
func jiraSanitizeSiteHost(siteURL string) string {
	if siteURL == "" {
		return ""
	}
	host := ""
	if u, err := parseURLHost(siteURL); err == nil && u != "" {
		host = u
	} else {
		host = jiraSchemeRe.ReplaceAllString(siteURL, "")
		host = strings.SplitN(host, "/", 2)[0]
	}
	if host == "" {
		return ""
	}
	return strings.ReplaceAll(strings.ToLower(host), ".", "_")
}

type jiraCacheCandidate struct {
	Path string
	Site string
}

// jiraResolveCandidatePaths ports resolveCandidatePaths: with an explicit
// site, returns a single (possibly non-existent) candidate; otherwise scans
// root for every site subdirectory that has a <key>.json file.
func jiraResolveCandidatePaths(root, key, explicitSite string) []jiraCacheCandidate {
	if explicitSite != "" {
		return []jiraCacheCandidate{{Path: filepath.Join(root, explicitSite, key+".json"), Site: explicitSite}}
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var matches []jiraCacheCandidate
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		candidate := filepath.Join(root, e.Name(), key+".json")
		if fileExists(candidate) {
			matches = append(matches, jiraCacheCandidate{Path: candidate, Site: e.Name()})
		}
	}
	return matches
}

// jiraGetCachePath ports getCachePath: ensures cacheDir exists and, only
// when cacheDir resolves inside the current working tree, seeds a
// .gitignore ("*\n") so an explicit --cache-dir used inside a repo doesn't
// get accidentally committed.
func jiraGetCachePath(key, cacheDir string) (string, error) {
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return "", fmt.Errorf("jira: create cache dir: %w", err)
	}
	if cwd, err := os.Getwd(); err == nil {
		if rel, relErr := filepath.Rel(cwd, cacheDir); relErr == nil {
			insideWorkTree := rel != "" && rel != "." && !strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel)
			if insideWorkTree {
				gitignorePath := filepath.Join(cacheDir, ".gitignore")
				if !fileExists(gitignorePath) {
					_ = os.WriteFile(gitignorePath, []byte("*\n"), 0o644)
				}
			}
		}
	}
	return filepath.Join(cacheDir, key+".json"), nil
}

// jiraCacheResolution mirrors jira.js's resolveEffectiveCachePath return
// shape ({ path, warnings, candidateSites }); Path == "" means no cache
// could be resolved.
type jiraCacheResolution struct {
	Path           string
	Warnings       []string
	CandidateSites []string
}

// jiraResolveEffectiveCachePath ports resolveEffectiveCachePath, minus the
// legacy-cache auto-migration branch (dropped — see file-level deviation #1).
func jiraResolveEffectiveCachePath(key, cacheDir, site string) (jiraCacheResolution, error) {
	var res jiraCacheResolution

	if cacheDir != "" {
		p, err := jiraGetCachePath(key, cacheDir)
		if err != nil {
			return res, err
		}
		res.Path = p
		return res, nil
	}

	root := jiraHomeCacheRoot()
	matches := jiraResolveCandidatePaths(root, key, site)

	if site != "" {
		if _, err := os.Stat(root); err != nil {
			res.Warnings = append(res.Warnings, fmt.Sprintf(
				"Home cache root %s does not exist. Run cache initialization first (omit site or use force-refresh).", root))
			return res, nil
		}
		res.Path = matches[0].Path
		return res, nil
	}

	if len(matches) == 1 {
		res.Path = matches[0].Path
		return res, nil
	}

	if len(matches) >= 2 {
		sites := make([]string, len(matches))
		for i, m := range matches {
			sites[i] = m.Site
		}
		res.Warnings = append(res.Warnings, fmt.Sprintf(
			"Cache key '%s' exists under multiple sites: %s. Pass site to disambiguate.", key, strings.Join(sites, ", ")))
		res.CandidateSites = sites
		return res, nil
	}

	// No home matches. jira.js falls back to probing/migrating
	// .sdlc/jira-cache and .claude/jira-cache here; that legacy-migration
	// path is dropped in this port (deviation #1) — a fresh miss is
	// reported instead.
	return res, nil
}

// ---------------------------------------------------------------------------
// Project config / membership (mirrors loadJiraConfig / validateProjectMembership)
// ---------------------------------------------------------------------------

func jiraLoadJiraConfig(mainRoot string) map[string]any {
	section, err := config.ReadSection(mainRoot, "jira")
	if err != nil {
		return map[string]any{}
	}
	return section
}

func jiraValidateProjectMembership(key string, jiraConfig map[string]any) string {
	raw, ok := jiraConfig["projects"]
	if !ok {
		return ""
	}
	list, ok := raw.([]any)
	if !ok || len(list) < 2 {
		return ""
	}
	names := make([]string, 0, len(list))
	found := false
	for _, v := range list {
		s, _ := v.(string)
		names = append(names, s)
		if s == key {
			found = true
		}
	}
	if found {
		return ""
	}
	return fmt.Sprintf("Project %s is not in jira.projects: [%s]", key, strings.Join(names, ", "))
}

// ---------------------------------------------------------------------------
// Templates directory resolution (mirrors findPluginInstalls / resolveTemplatesDir)
// ---------------------------------------------------------------------------

var (
	jiraTemplateInstallsOnce sync.Once
	jiraTemplateInstalls     []string
)

// jiraFindPluginTemplateInstalls walks ~/.claude/plugins looking for a
// directory literally named "templates" whose parent directory is named
// "jira-sdlc" (jira.js's findPluginInstalls). Cached for the process
// lifetime via sync.Once, mirroring the staleness caveat already documented
// for plan.go's analogous skill-template-index cache: a plugin
// install/update mid-process will not be picked up until restart.
func jiraFindPluginTemplateInstalls() []string {
	jiraTemplateInstallsOnce.Do(func() {
		home, err := os.UserHomeDir()
		if err != nil {
			return
		}
		pluginsRoot := filepath.Join(home, ".claude", "plugins")
		var results []string
		var walk func(dir string, depth int)
		walk = func(dir string, depth int) {
			if depth > 6 {
				return
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				return
			}
			for _, e := range entries {
				if !e.IsDir() {
					continue
				}
				full := filepath.Join(dir, e.Name())
				if e.Name() == "templates" && filepath.Base(dir) == "jira-sdlc" {
					results = append(results, full)
				} else {
					walk(full, depth+1)
				}
			}
		}
		walk(pluginsRoot, 0)
		jiraTemplateInstalls = results
	})
	return jiraTemplateInstalls
}

func jiraResolveTemplatesDir(override string) string {
	if override != "" {
		return override
	}
	if installs := jiraFindPluginTemplateInstalls(); len(installs) > 0 {
		return installs[0]
	}
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}
	return filepath.Join(cwd, "plugins", "sdlc-utilities", "skills", "jira-sdlc", "templates")
}

// ---------------------------------------------------------------------------
// Template status resolution (mirrors resolveTemplateStatus)
// ---------------------------------------------------------------------------

// jiraTemplateFallbackMap ports TEMPLATE_FALLBACK_MAP (spec R18).
var jiraTemplateFallbackMap = map[string]string{
	"Sub-bug":  "Bug",
	"Sub-task": "Task",
	"Subtask":  "Task",
}

type jiraCustomTemplate struct {
	Path   string `json:"path"`
	Exists bool   `json:"exists"`
}

type jiraTemplateFallback struct {
	Type       string `json:"type"`
	FallbackTo string `json:"fallbackTo"`
}

// jiraResolveTemplateStatus ports resolveTemplateStatus. mainRoot anchors
// the custom-templates directory at .sdlc/jira-templates (jira.js's
// resolveSdlcRoot()-rooted customDir, R-projectroot #360). The legacy
// .claude/jira-templates/ migration (runAutoMigration) is dropped — see
// file-level deviation #2.
func jiraResolveTemplateStatus(mainRoot, cachePath, templatesDir string) map[string]any {
	issueTypes := []string{}
	if cachePath != "" && fileExists(cachePath) {
		var cache map[string]any
		if err := fsx.ReadJSON(cachePath, &cache); err == nil {
			if it, ok := cache["issueTypes"].(map[string]any); ok {
				for k := range it {
					issueTypes = append(issueTypes, k)
				}
				sort.Strings(issueTypes) // see deviation #9: Go maps have no stable order.
			}
		}
	}

	customDir := filepath.Join(mainRoot, ".sdlc", "jira-templates")

	defaultTemplateNames := []string{}
	if entries, err := os.ReadDir(templatesDir); err == nil {
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
				defaultTemplateNames = append(defaultTemplateNames, strings.TrimSuffix(e.Name(), ".md"))
			}
		}
	}
	containsStr := func(list []string, v string) bool {
		for _, s := range list {
			if s == v {
				return true
			}
		}
		return false
	}

	customTemplates := map[string]any{}
	resolved := map[string]any{}
	fallbacks := []jiraTemplateFallback{}
	noneTypes := []string{}

	for _, issueType := range issueTypes {
		customPath := filepath.Join(customDir, issueType+".md")
		exists := fileExists(customPath)
		customTemplates[issueType] = jiraCustomTemplate{Path: customPath, Exists: exists}

		switch {
		case exists:
			resolved[issueType] = "custom"
		case containsStr(defaultTemplateNames, issueType):
			resolved[issueType] = "default"
		default:
			if parent, ok := jiraTemplateFallbackMap[issueType]; ok && containsStr(defaultTemplateNames, parent) {
				resolved[issueType] = "default-fallback"
				fallbacks = append(fallbacks, jiraTemplateFallback{Type: issueType, FallbackTo: parent})
			} else {
				resolved[issueType] = "none"
				noneTypes = append(noneTypes, issueType)
			}
		}
	}

	return map[string]any{
		"issueTypes":       issueTypes,
		"customTemplates":  customTemplates,
		"defaultTemplates": defaultTemplateNames,
		"resolved":         resolved,
		"fallbacks":        fallbacks,
		"noneTypes":        noneTypes,
	}
}

// ---------------------------------------------------------------------------
// Small helpers
// ---------------------------------------------------------------------------

func jiraStrs(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func jiraNullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// jiraTruthy mirrors JS Boolean(x) for JSON-decoded values.
func jiraTruthy(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case bool:
		return t
	case string:
		return t != ""
	case float64:
		return t != 0
	default:
		return true
	}
}

func jiraCopyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o644)
}

// parseURLHost extracts the host component from an absolute URL, mirroring
// JS's `new URL(x).host`. Returns ("", err) when siteURL has no scheme//host
// (net/url happily "parses" bare hosts as a relative path, leaving Host
// empty — treated here as a parse failure so callers fall back to manual
// scheme-stripping, matching jira.js's try/catch fallback).
func parseURLHost(siteURL string) (string, error) {
	u, err := url.Parse(siteURL)
	if err != nil {
		return "", err
	}
	if u.Host == "" {
		return "", fmt.Errorf("jira: no host in %q", siteURL)
	}
	return u.Host, nil
}

// ---------------------------------------------------------------------------
// Action: check
// ---------------------------------------------------------------------------

func jiraCheck(mainRoot string, in JiraIn) (any, error) {
	key := strings.ToUpper(strings.TrimSpace(in.Key))
	jiraConfig := jiraLoadJiraConfig(mainRoot)
	if msg := jiraValidateProjectMembership(key, jiraConfig); msg != "" {
		return nil, &mcpserver.DomainError{Msg: msg}
	}

	resolved, err := jiraResolveEffectiveCachePath(key, in.CacheDir, in.Site)
	if err != nil {
		return nil, &mcpserver.InfraError{Msg: "resolve cache path: " + err.Error(), Cause: err}
	}

	// flags.skipWorkflowDiscovery is a dead passthrough in jira.js itself
	// (never read after being echoed) — dropped rather than wired to a
	// tenth input field. See deviation #4.
	flagsBlock := map[string]any{
		"skipWorkflowDiscovery": false,
		"site":                  jiraNullableString(in.Site),
	}

	if resolved.Path == "" {
		return map[string]any{
			"exists":         false,
			"fresh":          false,
			"projectKey":     key,
			"cachePath":      nil,
			"candidateSites": jiraStrs(resolved.CandidateSites),
			"missing":        []string{"all"},
			"flags":          flagsBlock,
			"errors":         []string{},
			"warnings":       jiraStrs(resolved.Warnings),
		}, nil
	}

	if !fileExists(resolved.Path) {
		return map[string]any{
			"exists":         false,
			"fresh":          false,
			"projectKey":     key,
			"cachePath":      resolved.Path,
			"candidateSites": []string{},
			"missing":        []string{"all"},
			"flags":          flagsBlock,
			"errors":         []string{},
			"warnings":       jiraStrs(resolved.Warnings),
		}, nil
	}

	var cache map[string]any
	if err := fsx.ReadJSON(resolved.Path, &cache); err != nil {
		return map[string]any{
			"exists":     true,
			"fresh":      false,
			"projectKey": key,
			"cachePath":  resolved.Path,
			"missing":    []string{"all"},
			"flags":      flagsBlock,
			"errors":     []string{fmt.Sprintf("Cache file is not valid JSON: %s", err.Error())},
			"warnings":   jiraStrs(resolved.Warnings),
		}, nil
	}

	maxAgeHours := 0.0
	if v, ok := cache["maxAgeHours"].(float64); ok {
		maxAgeHours = v
	}

	var ageHoursPtr *float64
	fresh := false
	if luRaw, ok := cache["lastUpdated"]; ok {
		if luStr, ok := luRaw.(string); ok && luStr != "" {
			t, perr := time.Parse(time.RFC3339, luStr)
			if perr != nil {
				t, perr = time.Parse(time.RFC3339Nano, luStr)
			}
			if perr == nil {
				ageHours := time.Since(t).Hours()
				rounded := math.Round(ageHours*100) / 100
				ageHoursPtr = &rounded
				if maxAgeHours == 0 {
					fresh = true
				} else {
					fresh = ageHours < maxAgeHours
				}
			}
		}
	}

	issueTypesMap, _ := cache["issueTypes"].(map[string]any)
	fieldSchemas, _ := cache["fieldSchemas"].(map[string]any)
	workflows, _ := cache["workflows"].(map[string]any)
	linkTypes, _ := cache["linkTypes"].([]any)
	userMappings, _ := cache["userMappings"].(map[string]any)

	issueTypeNames := make([]string, 0, len(issueTypesMap))
	for k := range issueTypesMap {
		issueTypeNames = append(issueTypeNames, k)
	}
	sort.Strings(issueTypeNames)

	issueTypesWithWorkflows := 0
	incompleteWorkflows := []string{}
	if workflows != nil {
		issueTypesWithWorkflows = len(workflows)
		for _, name := range issueTypeNames {
			if _, ok := workflows[name]; !ok {
				incompleteWorkflows = append(incompleteWorkflows, name)
			}
		}
	} else {
		incompleteWorkflows = append(incompleteWorkflows, issueTypeNames...)
	}

	sections := map[string]any{
		"cloudId":      map[string]any{"present": jiraTruthy(cache["cloudId"])},
		"currentUser":  map[string]any{"present": jiraTruthy(cache["currentUser"])},
		"project":      map[string]any{"present": jiraTruthy(cache["project"])},
		"issueTypes":   map[string]any{"present": issueTypesMap != nil, "count": len(issueTypeNames)},
		"fieldSchemas": map[string]any{"present": fieldSchemas != nil, "issueTypesWithSchemas": len(fieldSchemas)},
		"workflows":    map[string]any{"present": workflows != nil, "issueTypesWithWorkflows": issueTypesWithWorkflows, "incomplete": jiraStrs(incompleteWorkflows)},
		"linkTypes":    map[string]any{"present": linkTypes != nil, "count": len(linkTypes)},
		"userMappings": map[string]any{"present": userMappings != nil, "count": len(userMappings)},
	}

	sectionOrder := []string{"cloudId", "currentUser", "project", "issueTypes", "fieldSchemas", "workflows", "linkTypes", "userMappings"}
	missing := []string{}
	for _, name := range sectionOrder {
		info := sections[name].(map[string]any)
		if present, _ := info["present"].(bool); !present {
			missing = append(missing, name)
		}
	}

	templatesDir := jiraResolveTemplatesDir(in.TemplatesDir)
	templateStatus := jiraResolveTemplateStatus(mainRoot, resolved.Path, templatesDir)
	resolvedTypes, _ := templateStatus["resolved"].(map[string]any)
	customTypes := []string{}
	for k, v := range resolvedTypes {
		if s, ok := v.(string); ok && s == "custom" {
			customTypes = append(customTypes, k)
		}
	}
	sort.Strings(customTypes)
	noneTypes, _ := templateStatus["noneTypes"].([]string)
	fallbacks := templateStatus["fallbacks"]
	defaultTemplates, _ := templateStatus["defaultTemplates"].([]string)

	warnings := append([]string{}, resolved.Warnings...)
	if ageHoursPtr == nil {
		warnings = append(warnings, "Cache file has no lastUpdated field; freshness cannot be determined.")
	}
	if len(incompleteWorkflows) > 0 {
		warnings = append(warnings, fmt.Sprintf("Workflow data missing for issue types: %s", strings.Join(incompleteWorkflows, ", ")))
	}

	var ageHoursOut any
	if ageHoursPtr != nil {
		ageHoursOut = *ageHoursPtr
	}

	return map[string]any{
		"exists":         true,
		"fresh":          fresh,
		"ageHours":       ageHoursOut,
		"maxAgeHours":    maxAgeHours,
		"projectKey":     key,
		"cachePath":      resolved.Path,
		"candidateSites": []string{},
		"sections":       sections,
		"templates": map[string]any{
			"customCount":    len(customTypes),
			"defaultCount":   len(defaultTemplates),
			"customTypes":    jiraStrs(customTypes),
			"uncoveredTypes": jiraStrs(noneTypes),
			"fallbacks":      fallbacks,
		},
		"missing":  jiraStrs(missing),
		"flags":    flagsBlock,
		"errors":   []string{},
		"warnings": jiraStrs(warnings),
	}, nil
}

// ---------------------------------------------------------------------------
// Action: load
// ---------------------------------------------------------------------------

func jiraLoad(mainRoot string, in JiraIn) (any, error) {
	key := strings.ToUpper(strings.TrimSpace(in.Key))
	resolved, err := jiraResolveEffectiveCachePath(key, in.CacheDir, in.Site)
	if err != nil {
		return nil, &mcpserver.InfraError{Msg: "resolve cache path: " + err.Error(), Cause: err}
	}
	if resolved.Path == "" {
		if len(resolved.CandidateSites) >= 2 {
			return nil, &mcpserver.DomainError{
				Msg: fmt.Sprintf("multiple cache entries for '%s' — pass site to disambiguate", key),
			}
		}
		return nil, &mcpserver.DataError{Msg: "no cache found for project; run cache initialization first"}
	}
	if !fileExists(resolved.Path) {
		return nil, &mcpserver.DataError{Msg: "no cache found for project; run cache initialization first"}
	}
	var cache map[string]any
	if err := fsx.ReadJSON(resolved.Path, &cache); err != nil {
		return nil, &mcpserver.DataError{Msg: fmt.Sprintf("cache file is not valid JSON: %s", err.Error()), Cause: err}
	}
	return cache, nil
}

// ---------------------------------------------------------------------------
// Action: save
// ---------------------------------------------------------------------------

func jiraSave(mainRoot string, in JiraIn) (any, error) {
	key := strings.ToUpper(strings.TrimSpace(in.Key))
	data := in.Data
	if data == nil {
		data = map[string]any{}
	}

	var missingFields []string
	for _, f := range []string{"version", "cloudId", "project", "siteUrl"} {
		if _, ok := data[f]; !ok {
			missingFields = append(missingFields, f)
		}
	}
	if len(missingFields) > 0 {
		return nil, &mcpserver.DomainError{Msg: fmt.Sprintf("cache JSON is missing required fields: %s", strings.Join(missingFields, ", "))}
	}

	var writePath string
	if in.CacheDir != "" {
		p, err := jiraGetCachePath(key, in.CacheDir)
		if err != nil {
			return nil, &mcpserver.InfraError{Msg: err.Error(), Cause: err}
		}
		writePath = p
	} else {
		siteURL, _ := data["siteUrl"].(string)
		resolvedSite := in.Site
		if resolvedSite == "" {
			resolvedSite = jiraSanitizeSiteHost(siteURL)
		}
		if resolvedSite == "" {
			return nil, &mcpserver.DomainError{Msg: fmt.Sprintf("cannot derive site host from siteUrl: %v", data["siteUrl"])}
		}
		writePath = filepath.Join(jiraHomeCacheRoot(), resolvedSite, key+".json")
	}

	if err := os.MkdirAll(filepath.Dir(writePath), 0o755); err != nil {
		return nil, &mcpserver.InfraError{Msg: "create cache dir: " + err.Error(), Cause: err}
	}
	if err := fsx.AtomicWriteJSON(writePath, data); err != nil {
		return nil, &mcpserver.InfraError{Msg: "write cache file: " + err.Error(), Cause: err}
	}
	return map[string]any{"saved": true, "cachePath": writePath}, nil
}

// ---------------------------------------------------------------------------
// Action: save-field
// ---------------------------------------------------------------------------

func jiraSaveField(mainRoot string, in JiraIn) (any, error) {
	key := strings.ToUpper(strings.TrimSpace(in.Key))
	if in.FieldName == "" {
		return nil, &mcpserver.DomainError{Msg: "fieldName is required for save-field"}
	}
	if in.Data == nil {
		// jira.js can only reach its merge logic with a successfully-parsed
		// stdin JSON value; a typed caller that omits Data most likely made
		// a mistake, so this is rejected rather than silently nulling the
		// field. See deviation #5.
		return nil, &mcpserver.DomainError{Msg: "data is required for save-field"}
	}

	resolved, err := jiraResolveEffectiveCachePath(key, in.CacheDir, in.Site)
	if err != nil {
		return nil, &mcpserver.InfraError{Msg: "resolve cache path: " + err.Error(), Cause: err}
	}
	if resolved.Path == "" || !fileExists(resolved.Path) {
		return nil, &mcpserver.DataError{Msg: "no cache found for project; run cache initialization first"}
	}

	var cache map[string]any
	if err := fsx.ReadJSON(resolved.Path, &cache); err != nil {
		return nil, &mcpserver.DataError{Msg: fmt.Sprintf("cache file is not valid JSON: %s", err.Error()), Cause: err}
	}

	existing, existingIsObj := cache[in.FieldName].(map[string]any)
	if existingIsObj {
		merged := make(map[string]any, len(existing)+len(in.Data))
		for k, v := range existing {
			merged[k] = v
		}
		for k, v := range in.Data {
			merged[k] = v
		}
		cache[in.FieldName] = merged
	} else {
		cache[in.FieldName] = in.Data
	}

	if err := fsx.AtomicWriteJSON(resolved.Path, cache); err != nil {
		return nil, &mcpserver.InfraError{Msg: "write cache file: " + err.Error(), Cause: err}
	}
	return map[string]any{"saved": true, "field": in.FieldName, "cachePath": resolved.Path}, nil
}

// ---------------------------------------------------------------------------
// Action: templates
// ---------------------------------------------------------------------------

func jiraTemplates(mainRoot string, in JiraIn) (any, error) {
	key := strings.ToUpper(strings.TrimSpace(in.Key))
	resolved, err := jiraResolveEffectiveCachePath(key, in.CacheDir, in.Site)
	if err != nil {
		return nil, &mcpserver.InfraError{Msg: "resolve cache path: " + err.Error(), Cause: err}
	}
	templatesDir := jiraResolveTemplatesDir(in.TemplatesDir)
	return jiraResolveTemplateStatus(mainRoot, resolved.Path, templatesDir), nil
}

// ---------------------------------------------------------------------------
// Action: init-templates
// ---------------------------------------------------------------------------

func jiraInitTemplates(mainRoot string, in JiraIn) (any, error) {
	key := strings.ToUpper(strings.TrimSpace(in.Key))
	resolved, err := jiraResolveEffectiveCachePath(key, in.CacheDir, in.Site)
	if err != nil {
		return nil, &mcpserver.InfraError{Msg: "resolve cache path: " + err.Error(), Cause: err}
	}

	issueTypes := []string{}
	if resolved.Path != "" && fileExists(resolved.Path) {
		var cache map[string]any
		if err := fsx.ReadJSON(resolved.Path, &cache); err == nil {
			if it, ok := cache["issueTypes"].(map[string]any); ok {
				for k := range it {
					issueTypes = append(issueTypes, k)
				}
				sort.Strings(issueTypes)
			}
		}
	}

	templatesDir := jiraResolveTemplatesDir(in.TemplatesDir)
	customDir := filepath.Join(mainRoot, ".sdlc", "jira-templates")
	if err := os.MkdirAll(customDir, 0o755); err != nil {
		return nil, &mcpserver.InfraError{Msg: "create custom templates dir: " + err.Error(), Cause: err}
	}

	initialized := []string{}
	skipped := []string{}
	unavailable := []string{}

	for _, issueType := range issueTypes {
		dst := filepath.Join(customDir, issueType+".md")
		if fileExists(dst) {
			skipped = append(skipped, issueType)
			continue
		}
		src := filepath.Join(templatesDir, issueType+".md")
		if fileExists(src) {
			if err := jiraCopyFile(src, dst); err != nil {
				return nil, &mcpserver.InfraError{Msg: "copy template: " + err.Error(), Cause: err}
			}
			initialized = append(initialized, issueType)
		} else {
			unavailable = append(unavailable, issueType)
		}
	}

	return map[string]any{"initialized": initialized, "skipped": skipped, "unavailable": unavailable}, nil
}

// ---------------------------------------------------------------------------
// Action: clear
// ---------------------------------------------------------------------------

func jiraClear(mainRoot string, in JiraIn) (any, error) {
	key := strings.ToUpper(strings.TrimSpace(in.Key))
	if in.CacheDir != "" {
		p, err := jiraGetCachePath(key, in.CacheDir)
		if err != nil {
			return nil, &mcpserver.InfraError{Msg: err.Error(), Cause: err}
		}
		if fileExists(p) {
			if err := os.Remove(p); err != nil {
				return nil, &mcpserver.InfraError{Msg: "delete cache file: " + err.Error(), Cause: err}
			}
		}
		return map[string]any{"cleared": true, "cachePath": p}, nil
	}

	candidates := jiraResolveCandidatePaths(jiraHomeCacheRoot(), key, in.Site)
	cleared := []string{}
	for _, c := range candidates {
		if fileExists(c.Path) {
			if err := os.Remove(c.Path); err != nil {
				return nil, &mcpserver.InfraError{Msg: "delete cache file: " + err.Error(), Cause: err}
			}
			cleared = append(cleared, c.Path)
		}
	}
	return map[string]any{"cleared": true, "cachePaths": cleared}, nil
}

// ---------------------------------------------------------------------------
// Action: copy-template
// ---------------------------------------------------------------------------

func jiraCopyTemplate(mainRoot string, in JiraIn) (any, error) {
	if in.TemplateType == "" || in.TemplateFrom == "" {
		return nil, &mcpserver.DomainError{Msg: "templateType and templateFrom are required for copy-template"}
	}

	templatesDir := jiraResolveTemplatesDir(in.TemplatesDir)
	src := filepath.Join(templatesDir, in.TemplateFrom+".md")
	if !fileExists(src) {
		return nil, &mcpserver.DataError{Msg: fmt.Sprintf("template source not found: %s", src)}
	}

	dst := filepath.Join(mainRoot, ".sdlc", "jira-templates", in.TemplateType+".md")
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return nil, &mcpserver.InfraError{Msg: "create custom templates dir: " + err.Error(), Cause: err}
	}

	if fileExists(dst) {
		return map[string]any{"copied": false, "reason": "exists", "type": in.TemplateType, "destination": dst}, nil
	}

	if err := jiraCopyFile(src, dst); err != nil {
		return nil, &mcpserver.InfraError{Msg: "copy template: " + err.Error(), Cause: err}
	}
	return map[string]any{"copied": true, "type": in.TemplateType, "from": in.TemplateFrom, "destination": dst}, nil
}

// ---------------------------------------------------------------------------
// Action: validate-body
// ---------------------------------------------------------------------------

var (
	jiraURLRe           = regexp.MustCompile(`https?://[^\s)\]>"']+`)
	jiraTrailingPunctRe = regexp.MustCompile(`[.,;:!?]+$`)
	jiraLineSplitRe     = regexp.MustCompile(`\r?\n`)
)

type jiraExtractedURL struct {
	URL  string
	Line int
}

// jiraExtractUrls ports scripts/lib/links.js's extractUrls: scans line by
// line, dedupes by URL keeping the first-occurrence line number, strips
// trailing punctuation, and strips one trailing ')' when the URL contains
// no '('.
func jiraExtractUrls(text string) []jiraExtractedURL {
	if text == "" {
		return nil
	}
	lines := jiraLineSplitRe.Split(text, -1)
	seen := map[string]int{}
	var order []string
	for i, line := range lines {
		for _, m := range jiraURLRe.FindAllString(line, -1) {
			u := jiraTrailingPunctRe.ReplaceAllString(m, "")
			if strings.HasSuffix(u, ")") && !strings.Contains(u, "(") {
				u = u[:len(u)-1]
			}
			if _, ok := seen[u]; !ok {
				seen[u] = i + 1
				order = append(order, u)
			}
		}
	}
	out := make([]jiraExtractedURL, 0, len(order))
	for _, u := range order {
		out = append(out, jiraExtractedURL{URL: u, Line: seen[u]})
	}
	return out
}

// jiraFormatViolations ports scripts/lib/links.js's formatViolations.
func jiraFormatViolations(violations []jiraViolation) string {
	if len(violations) == 0 {
		return "No violations."
	}
	lines := []string{"Link verification failed:", ""}
	for _, v := range violations {
		where := ""
		if v.Line != 0 {
			where = fmt.Sprintf(" (line %d)", v.Line)
		}
		detailStr := ""
		if v.Detail != "" {
			detailStr = fmt.Sprintf(" — %s", v.Detail)
		}
		lines = append(lines, fmt.Sprintf("  - %s%s: %s%s", v.URL, where, v.Reason, detailStr))
	}
	lines = append(lines, "", "Remove or correct the listed URLs and retry.")
	return strings.Join(lines, "\n")
}

func jiraValidateBody(mainRoot string, in JiraIn, offline bool) (any, error) {
	extracted := jiraExtractUrls(in.MarkdownBody)
	urls := make([]string, len(extracted))
	lineOf := make(map[string]int, len(extracted))
	for i, e := range extracted {
		urls[i] = e.URL
		lineOf[e.URL] = e.Line
	}

	// See deviation #6: internal/links.Ctx has no jiraSite-override field,
	// so Site has no effect here; CacheDir is passed through as
	// JiraCacheDir so the site-sharded home-cache auto-discovery in
	// internal/links can see it (including a test's temp-dir override).
	ctx := links.Ctx{
		Offline:      offline,
		JiraCacheDir: in.CacheDir,
		RepoDir:      mainRoot,
	}
	results := links.Validate(ctx, urls)

	out := JiraValidateBodyOut{OK: true, Violations: []jiraViolation{}, Skipped: []jiraSkipped{}}
	for _, r := range results {
		line := lineOf[r.URL]
		switch r.Status {
		case "violation":
			out.OK = false
			out.Violations = append(out.Violations, jiraViolation{URL: r.URL, Line: line, Reason: r.Reason, Detail: r.Detail})
		case "skipped":
			out.Skipped = append(out.Skipped, jiraSkipped{URL: r.URL, Line: line, Reason: r.Reason})
		}
	}
	if !out.OK {
		out.Message = jiraFormatViolations(out.Violations)
	}

	if in.MarkdownBody != "" {
		adfDoc, err := adf.Convert(in.MarkdownBody)
		if err != nil {
			return nil, &mcpserver.InfraError{Msg: "convert markdown to ADF: " + err.Error(), Cause: err}
		}
		out.ADF = adfDoc
	}

	return out, nil
}

// ---------------------------------------------------------------------------
// Dispatch + registration
// ---------------------------------------------------------------------------

// jiraCore is the testable entry point. offline is threaded through
// explicitly (rather than read from the environment here) so tests can
// force validate-body's link check into offline mode without touching
// process env — see deviation #7.
func jiraCore(mainRoot string, in JiraIn, offline bool) (any, error) {
	if !in.SkipConfigCheck {
		if err := configmigrate.Verify(mainRoot); err != nil {
			return map[string]any{"errors": []string{fmt.Sprintf("config-version: %s", err.Error())}}, nil
		}
	}

	if in.Action != "validate-body" && strings.TrimSpace(in.Key) == "" {
		return nil, &mcpserver.DomainError{Msg: "key is required"}
	}

	switch in.Action {
	case "check":
		return jiraCheck(mainRoot, in)
	case "load":
		return jiraLoad(mainRoot, in)
	case "save":
		return jiraSave(mainRoot, in)
	case "save-field":
		return jiraSaveField(mainRoot, in)
	case "templates":
		return jiraTemplates(mainRoot, in)
	case "init-templates":
		return jiraInitTemplates(mainRoot, in)
	case "clear":
		return jiraClear(mainRoot, in)
	case "copy-template":
		return jiraCopyTemplate(mainRoot, in)
	case "validate-body":
		return jiraValidateBody(mainRoot, in, offline)
	default:
		return nil, &mcpserver.DomainError{Msg: fmt.Sprintf("unknown jira action %q", in.Action)}
	}
}

// RegisterJiraTools registers the jira tool. Registration only — wiring
// into runMCP's dispatch is Task 40's responsibility.
func RegisterJiraTools(s *mcpserver.Server) {
	mcpserver.Register(s, "jira",
		"Manage jira-sdlc cache and templates: check, load, save, save-field, templates, init-templates, clear, copy-template, validate-body",
		func(_ mcpserver.Ctx, in JiraIn) (any, error) {
			mainRoot, err := worktree.MainRoot()
			if err != nil {
				return nil, &mcpserver.InfraError{Msg: "resolve main root: " + err.Error(), Cause: err}
			}
			offline := os.Getenv("SDLC_LINKS_OFFLINE") == "1"
			return jiraCore(mainRoot, in, offline)
		},
	)
}
