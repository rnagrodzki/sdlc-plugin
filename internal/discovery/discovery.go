// Package discovery validates the plugin discovery and cross-reference chain.
// It is a Go port of scripts/lib/discovery.js in the sdlc-utilities plugin.
//
// The JS validator returns a rich object with pass/skip/fail statuses for each
// check; this Go port returns only []Finding — failed checks produce findings,
// passing and skipped checks produce nothing.
//
// Deviation from JS: the JS uses process.cwd()-relative paths in some detail
// messages (PD6, PD7); this port uses project-root-relative paths instead,
// which is stable across invocations and avoids temp-dir noise in tests.
//
// Check IDs:
//
//	PD1  marketplace-manifest-exists     — .claude-plugin/marketplace.json valid JSON
//	PD2  marketplace-schema-reference    — $schema field present
//	PD3  marketplace-required-fields     — name + plugins array
//	PD4  plugin-source-paths-valid       — each source has plugin.json
//	PD5  name-consistency               — marketplace name matches plugin.json name
//	PD6  plugin-required-fields         — name, description, version in plugin.json
//	PD7  semver-format                  — version is valid semver
//	PD8  commands-discoverable          — commands have frontmatter with description
//	PD9  command-skill-refs-valid       — skill names referenced in commands exist
//	PD10 command-script-refs-valid      — scripts referenced in commands exist
//	PD11 skills-discoverable            — skills have SKILL.md with name+description
//	PD12 skill-supporting-files-exist   — sibling .md files referenced in SKILL.md exist
//	PD13 skill-agent-refs-valid         — agents referenced in skills exist
//	PD14 skill-script-refs-valid        — scripts referenced in skills exist
//	PD15 hooks-valid-json               — hooks.json exists and parses
//	PD16 agents-discoverable            — agents have frontmatter with name+description+tools
package discovery

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/rnagrodzki/sdlc-plugin/internal/frontmatter"
)

// Finding represents a single validation finding from a failed check.
type Finding struct {
	ID       string `json:"id"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
	Path     string `json:"path"`
}

// ValidateAll runs all PD1–PD16 discovery checks against root and returns
// findings for checks that fail. Checks that pass or are skipped (because
// a prerequisite check failed) produce no findings.
func ValidateAll(root string) []Finding {
	var findings []Finding

	// PD1 — marketplace manifest
	marketplace, pd1 := checkPD1(root)
	findings = append(findings, pd1...)

	// PD2-PD3 — marketplace structure (depend on PD1 data)
	findings = append(findings, checkPD2(marketplace)...)
	findings = append(findings, checkPD3(marketplace)...)

	// PD4 — plugin source paths valid (depends on PD1 data)
	plugins, pd4 := checkPD4(root, marketplace)
	findings = append(findings, pd4...)

	// PD5-PD16 — per-plugin checks (depend on PD4 plugins list)
	findings = append(findings, checkPD5(plugins)...)
	findings = append(findings, checkPD6(plugins)...)
	findings = append(findings, checkPD7(plugins)...)
	findings = append(findings, checkPD8(plugins)...)
	findings = append(findings, checkPD9(plugins)...)
	findings = append(findings, checkPD10(plugins)...)
	findings = append(findings, checkPD11(plugins)...)
	findings = append(findings, checkPD12(plugins)...)
	findings = append(findings, checkPD13(plugins)...)
	findings = append(findings, checkPD14(plugins)...)
	findings = append(findings, checkPD15(plugins)...)
	findings = append(findings, checkPD16(plugins)...)

	return findings
}

// ---------------------------------------------------------------------------
// Internal types
// ---------------------------------------------------------------------------

type pluginInfo struct {
	entryName    string
	sourceDir    string // relative to project root (e.g., "." or "plugins/my-plugin")
	pluginDir    string // absolute path to source directory
	pluginData   map[string]any
	manifestPath string // relative to project root
}

// ---------------------------------------------------------------------------
// File system helpers
// ---------------------------------------------------------------------------

func readFileContent(p string) (string, bool) {
	data, err := os.ReadFile(p)
	if err != nil {
		return "", false
	}
	return string(data), true
}

func isFile(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}

func isDir(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

func listDir(p string) []string {
	entries, err := os.ReadDir(p)
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// jsTruthy mirrors JavaScript truthiness for values from JSON/YAML unmarshal.
// nil → false, "" → false, everything else → true. This matches the JS
// source's !value guards on string fields (name, description, version, etc.).
func jsTruthy(v any) bool {
	if v == nil {
		return false
	}
	if s, ok := v.(string); ok {
		return s != ""
	}
	return true
}

// ---------------------------------------------------------------------------
// Pattern extractors for cross-reference detection
// ---------------------------------------------------------------------------

// Mirrors JS RE_FIND_SCRIPT, RE_PATH_SCRIPT, RE_DIRECT_SCRIPT.
var (
	reFindScript   = regexp.MustCompile("find\\s[^`\n]*?-name\\s+[\"']([^\"'<>]+\\.js)[\"']")
	rePathScript   = regexp.MustCompile("-path\\s+[\"']\\*/sdlc\\*/scripts/([^\\s\"'<>]+\\.js)[\"']")
	reDirectScript = regexp.MustCompile("plugins/sdlc-utilities/scripts/([^\\s\"'<>]+\\.js)")
)

func extractScriptRefs(content string) []string {
	var result []string
	seen := map[string]bool{}

	// Prefer -path match (includes subdirectory) over -name match (bare filename).
	for _, m := range rePathScript.FindAllStringSubmatch(content, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			result = append(result, m[1])
		}
	}
	for _, m := range reDirectScript.FindAllStringSubmatch(content, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			result = append(result, m[1])
		}
	}
	// Fall back to -name for patterns that lack -path.
	for _, m := range reFindScript.FindAllStringSubmatch(content, -1) {
		basename := m[1]
		alreadyCaptured := false
		for _, n := range result {
			if n == basename || strings.HasSuffix(n, "/"+basename) {
				alreadyCaptured = true
				break
			}
		}
		if !alreadyCaptured && !seen[basename] {
			seen[basename] = true
			result = append(result, basename)
		}
	}
	return result
}

// Mirrors JS RE_INVOKE_SKILL.
var reInvokeSkill = regexp.MustCompile("Invoke the `([^`]+)` skill")

func extractSkillRefs(content string) []string {
	var result []string
	seen := map[string]bool{}
	for _, m := range reInvokeSkill.FindAllStringSubmatch(content, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			result = append(result, m[1])
		}
	}
	return result
}

// Mirrors JS RE_AGENTS_PATH, RE_AGENT_BACKTICK.
var (
	reAgentsPath    = regexp.MustCompile("agents/([a-z][a-z0-9-]+)")
	reAgentBacktick = regexp.MustCompile("`([a-z][a-z0-9-]+)`\\s+agent")
)

func extractAgentRefs(content string) []string {
	var result []string
	seen := map[string]bool{}
	for _, m := range reAgentsPath.FindAllStringSubmatch(content, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			result = append(result, m[1])
		}
	}
	for _, m := range reAgentBacktick.FindAllStringSubmatch(content, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			result = append(result, m[1])
		}
	}
	return result
}

// Mirrors JS RE_SIBLING_MD and NON_SIBLING_MD.
// Go regexp does not support lookbehind/lookahead, so we check adjacent
// characters manually to emulate the JS negative assertions.
var reSiblingMD = regexp.MustCompile("`([A-Z][A-Z0-9_-]*\\.md)`")

var nonSiblingMD = map[string]bool{
	"CHANGELOG.md": true,
	"README.md":    true,
	"LICENSE.md":   true,
	"CLAUDE.md":    true,
	"SKILL.md":     true,
}

func extractSiblingFileRefs(content string) []string {
	var result []string
	seen := map[string]bool{}
	for _, loc := range reSiblingMD.FindAllStringSubmatchIndex(content, -1) {
		start := loc[0]
		end := loc[1]
		name := content[loc[2]:loc[3]]

		// Emulate negative lookbehind: skip double backtick before.
		if start > 0 && content[start-1] == '`' {
			continue
		}
		// Emulate negative lookahead: skip double backtick after.
		if end < len(content) && content[end] == '`' {
			continue
		}
		if !nonSiblingMD[name] && !seen[name] {
			seen[name] = true
			result = append(result, name)
		}
	}
	return result
}

// ---------------------------------------------------------------------------
// Individual checks
// ---------------------------------------------------------------------------

const marketplaceRel = ".claude-plugin/marketplace.json"

func checkPD1(root string) (map[string]any, []Finding) {
	filePath := filepath.Join(root, ".claude-plugin", "marketplace.json")

	if !isFile(filePath) {
		return nil, []Finding{{
			ID: "PD1", Severity: "error",
			Message: marketplaceRel + " not found",
			Path:    marketplaceRel,
		}}
	}

	content, ok := readFileContent(filePath)
	if !ok {
		return nil, []Finding{{
			ID: "PD1", Severity: "error",
			Message: marketplaceRel + " is not readable",
			Path:    marketplaceRel,
		}}
	}

	var data map[string]any
	if err := json.Unmarshal([]byte(content), &data); err != nil {
		return nil, []Finding{{
			ID: "PD1", Severity: "error",
			Message: marketplaceRel + " contains invalid JSON",
			Path:    marketplaceRel,
		}}
	}

	return data, nil
}

func checkPD2(marketplace map[string]any) []Finding {
	if marketplace == nil {
		return nil // skip — PD1 failed
	}
	if !jsTruthy(marketplace["$schema"]) {
		return []Finding{{
			ID: "PD2", Severity: "warning",
			Message: "$schema field missing from marketplace.json",
			Path:    marketplaceRel,
		}}
	}
	return nil
}

func checkPD3(marketplace map[string]any) []Finding {
	if marketplace == nil {
		return nil // skip — PD1 failed
	}
	var findings []Finding
	if !jsTruthy(marketplace["name"]) {
		findings = append(findings, Finding{
			ID: "PD3", Severity: "error",
			Message: "Missing required field: name",
			Path:    marketplaceRel,
		})
	}
	plugins, ok := marketplace["plugins"]
	if !ok {
		findings = append(findings, Finding{
			ID: "PD3", Severity: "error",
			Message: "Missing or empty required field: plugins (array)",
			Path:    marketplaceRel,
		})
	} else {
		arr, isArr := plugins.([]any)
		if !isArr || len(arr) == 0 {
			findings = append(findings, Finding{
				ID: "PD3", Severity: "error",
				Message: "Missing or empty required field: plugins (array)",
				Path:    marketplaceRel,
			})
		}
	}
	return findings
}

func checkPD4(root string, marketplace map[string]any) ([]pluginInfo, []Finding) {
	if marketplace == nil {
		return nil, nil // skip — PD1/PD3 failed
	}
	pluginsRaw, ok := marketplace["plugins"]
	if !ok {
		return nil, nil
	}
	pluginsArr, isArr := pluginsRaw.([]any)
	if !isArr {
		return nil, nil
	}

	var findings []Finding
	var validPlugins []pluginInfo

	for _, raw := range pluginsArr {
		entry, isMap := raw.(map[string]any)
		if !isMap {
			findings = append(findings, Finding{
				ID: "PD4", Severity: "error",
				Message: "Plugin entry is not an object",
				Path:    marketplaceRel,
			})
			continue
		}

		name, _ := entry["name"].(string)
		source, _ := entry["source"].(string)
		if !jsTruthy(name) || !jsTruthy(source) {
			entryJSON, _ := json.Marshal(entry)
			findings = append(findings, Finding{
				ID: "PD4", Severity: "error",
				Message: fmt.Sprintf("Plugin entry missing name or source: %s", string(entryJSON)),
				Path:    marketplaceRel,
			})
			continue
		}

		sourcePath := strings.TrimPrefix(source, "./")
		pluginDir := filepath.Join(root, sourcePath)
		manifestRel := filepath.Join(sourcePath, ".claude-plugin", "plugin.json")
		manifestAbs := filepath.Join(pluginDir, ".claude-plugin", "plugin.json")

		if !isDir(pluginDir) {
			findings = append(findings, Finding{
				ID: "PD4", Severity: "error",
				Message: fmt.Sprintf("Plugin %q: source directory not found: %s", name, sourcePath),
				Path:    sourcePath,
			})
			continue
		}
		if !isFile(manifestAbs) {
			findings = append(findings, Finding{
				ID: "PD4", Severity: "error",
				Message: fmt.Sprintf("Plugin %q: .claude-plugin/plugin.json not found in %s", name, sourcePath),
				Path:    manifestRel,
			})
			continue
		}

		content, ok := readFileContent(manifestAbs)
		if !ok {
			findings = append(findings, Finding{
				ID: "PD4", Severity: "error",
				Message: fmt.Sprintf("Plugin %q: plugin.json is not readable", name),
				Path:    manifestRel,
			})
			continue
		}

		var pluginData map[string]any
		if err := json.Unmarshal([]byte(content), &pluginData); err != nil {
			findings = append(findings, Finding{
				ID: "PD4", Severity: "error",
				Message: fmt.Sprintf("Plugin %q: plugin.json is invalid JSON — %s", name, err.Error()),
				Path:    manifestRel,
			})
			continue
		}

		validPlugins = append(validPlugins, pluginInfo{
			entryName:    name,
			sourceDir:    sourcePath,
			pluginDir:    pluginDir,
			pluginData:   pluginData,
			manifestPath: manifestRel,
		})
	}

	return validPlugins, findings
}

func checkPD5(plugins []pluginInfo) []Finding {
	if len(plugins) == 0 {
		return nil // skip — PD4 failed
	}
	var findings []Finding
	for _, p := range plugins {
		pluginName, _ := p.pluginData["name"].(string)
		if p.entryName != pluginName {
			findings = append(findings, Finding{
				ID: "PD5", Severity: "error",
				Message: fmt.Sprintf(
					"Plugin entry name %q in marketplace.json does not match "+
						"plugin.json name %q "+
						"— this causes \"plugin not found\" on update",
					p.entryName, pluginName),
				Path: p.manifestPath,
			})
		}
	}
	return findings
}

func checkPD6(plugins []pluginInfo) []Finding {
	if len(plugins) == 0 {
		return nil // skip — PD4 failed
	}
	var findings []Finding
	for _, p := range plugins {
		if !jsTruthy(p.pluginData["name"]) {
			findings = append(findings, Finding{
				ID: "PD6", Severity: "error",
				Message: fmt.Sprintf("%s: missing required field \"name\"", p.manifestPath),
				Path:    p.manifestPath,
			})
		}
		if !jsTruthy(p.pluginData["description"]) {
			findings = append(findings, Finding{
				ID: "PD6", Severity: "error",
				Message: fmt.Sprintf("%s: missing required field \"description\"", p.manifestPath),
				Path:    p.manifestPath,
			})
		}
		if !jsTruthy(p.pluginData["version"]) {
			findings = append(findings, Finding{
				ID: "PD6", Severity: "error",
				Message: fmt.Sprintf("%s: missing required field \"version\"", p.manifestPath),
				Path:    p.manifestPath,
			})
		}
	}
	return findings
}

var reSemver = regexp.MustCompile(`^\d+\.\d+\.\d+(-[a-zA-Z0-9.]+)?$`)

func checkPD7(plugins []pluginInfo) []Finding {
	if len(plugins) == 0 {
		return nil // skip — PD4 failed
	}
	var findings []Finding
	for _, p := range plugins {
		version, _ := p.pluginData["version"].(string)
		// Mirror JS guard: pluginData.version && !RE_SEMVER.test(...)
		// Empty-string version is falsy in JS, so it stays silent here
		// (and fires PD6 instead).
		if version != "" && !reSemver.MatchString(version) {
			findings = append(findings, Finding{
				ID: "PD7", Severity: "error",
				Message: fmt.Sprintf(
					"%s: version %q is not valid semver (expected X.Y.Z or X.Y.Z-pre)",
					p.manifestPath, version),
				Path: p.manifestPath,
			})
		}
	}
	return findings
}

func checkPD8(plugins []pluginInfo) []Finding {
	if len(plugins) == 0 {
		return nil // skip — PD4 failed
	}
	var findings []Finding
	for _, p := range plugins {
		cmdDir := filepath.Join(p.pluginDir, "commands")
		for _, f := range listDir(cmdDir) {
			if !strings.HasSuffix(f, ".md") {
				continue
			}
			content, ok := readFileContent(filepath.Join(cmdDir, f))
			if !ok {
				findings = append(findings, Finding{
					ID: "PD8", Severity: "error",
					Message: fmt.Sprintf("%s/commands/%s: cannot read file", p.entryName, f),
					Path:    filepath.Join(p.sourceDir, "commands", f),
				})
				continue
			}
			meta, _, err := frontmatter.Parse([]byte(content))
			if err != nil {
				findings = append(findings, Finding{
					ID: "PD8", Severity: "error",
					Message: fmt.Sprintf("%s/commands/%s: missing YAML frontmatter (--- delimiters)", p.entryName, f),
					Path:    filepath.Join(p.sourceDir, "commands", f),
				})
				continue
			}
			if !jsTruthy(meta["description"]) {
				findings = append(findings, Finding{
					ID: "PD8", Severity: "error",
					Message: fmt.Sprintf("%s/commands/%s: frontmatter missing required \"description\" field", p.entryName, f),
					Path:    filepath.Join(p.sourceDir, "commands", f),
				})
			}
		}
	}
	return findings
}

func checkPD9(plugins []pluginInfo) []Finding {
	if len(plugins) == 0 {
		return nil // skip — PD4 failed
	}
	var findings []Finding
	for _, p := range plugins {
		cmdDir := filepath.Join(p.pluginDir, "commands")
		skillDir := filepath.Join(p.pluginDir, "skills")
		for _, f := range listDir(cmdDir) {
			if !strings.HasSuffix(f, ".md") {
				continue
			}
			content, ok := readFileContent(filepath.Join(cmdDir, f))
			if !ok {
				continue
			}
			for _, skillName := range extractSkillRefs(content) {
				skillPath := filepath.Join(skillDir, skillName, "SKILL.md")
				if !isFile(skillPath) {
					findings = append(findings, Finding{
						ID: "PD9", Severity: "error",
						Message: fmt.Sprintf(
							"%s/commands/%s: references skill %q but skills/%s/SKILL.md does not exist",
							p.entryName, f, skillName, skillName),
						Path: filepath.Join(p.sourceDir, "commands", f),
					})
				}
			}
		}
	}
	return findings
}

func checkPD10(plugins []pluginInfo) []Finding {
	if len(plugins) == 0 {
		return nil // skip — PD4 failed
	}
	var findings []Finding
	for _, p := range plugins {
		cmdDir := filepath.Join(p.pluginDir, "commands")
		scriptDir := filepath.Join(p.pluginDir, "scripts")
		for _, f := range listDir(cmdDir) {
			if !strings.HasSuffix(f, ".md") {
				continue
			}
			content, ok := readFileContent(filepath.Join(cmdDir, f))
			if !ok {
				continue
			}
			for _, scriptName := range extractScriptRefs(content) {
				scriptPath := filepath.Join(scriptDir, scriptName)
				if !isFile(scriptPath) {
					findings = append(findings, Finding{
						ID: "PD10", Severity: "error",
						Message: fmt.Sprintf(
							"%s/commands/%s: references script %q but scripts/%s does not exist",
							p.entryName, f, scriptName, scriptName),
						Path: filepath.Join(p.sourceDir, "commands", f),
					})
				}
			}
		}
	}
	return findings
}

func checkPD11(plugins []pluginInfo) []Finding {
	if len(plugins) == 0 {
		return nil // skip — PD4 failed
	}
	var findings []Finding
	for _, p := range plugins {
		skillsDir := filepath.Join(p.pluginDir, "skills")
		for _, d := range listDir(skillsDir) {
			if !isDir(filepath.Join(skillsDir, d)) {
				continue
			}
			skillFile := filepath.Join(skillsDir, d, "SKILL.md")
			if !isFile(skillFile) {
				findings = append(findings, Finding{
					ID: "PD11", Severity: "error",
					Message: fmt.Sprintf("%s/skills/%s: SKILL.md is missing", p.entryName, d),
					Path:    filepath.Join(p.sourceDir, "skills", d, "SKILL.md"),
				})
				continue
			}
			content, ok := readFileContent(skillFile)
			if !ok {
				findings = append(findings, Finding{
					ID: "PD11", Severity: "error",
					Message: fmt.Sprintf("%s/skills/%s/SKILL.md: cannot read", p.entryName, d),
					Path:    filepath.Join(p.sourceDir, "skills", d, "SKILL.md"),
				})
				continue
			}
			meta, _, err := frontmatter.Parse([]byte(content))
			if err != nil {
				// Map any parse error (including yaml.v3 decode errors) to
				// the same "missing YAML frontmatter" finding the JS emits.
				findings = append(findings, Finding{
					ID: "PD11", Severity: "error",
					Message: fmt.Sprintf("%s/skills/%s/SKILL.md: missing YAML frontmatter", p.entryName, d),
					Path:    filepath.Join(p.sourceDir, "skills", d, "SKILL.md"),
				})
				continue
			}
			if !jsTruthy(meta["name"]) {
				findings = append(findings, Finding{
					ID: "PD11", Severity: "error",
					Message: fmt.Sprintf("%s/skills/%s/SKILL.md: frontmatter missing \"name\"", p.entryName, d),
					Path:    filepath.Join(p.sourceDir, "skills", d, "SKILL.md"),
				})
			}
			if !jsTruthy(meta["description"]) {
				findings = append(findings, Finding{
					ID: "PD11", Severity: "error",
					Message: fmt.Sprintf("%s/skills/%s/SKILL.md: frontmatter missing \"description\"", p.entryName, d),
					Path:    filepath.Join(p.sourceDir, "skills", d, "SKILL.md"),
				})
			}
		}
	}
	return findings
}

func checkPD12(plugins []pluginInfo) []Finding {
	if len(plugins) == 0 {
		return nil // skip — PD4 failed
	}
	var findings []Finding
	for _, p := range plugins {
		skillsDir := filepath.Join(p.pluginDir, "skills")
		for _, d := range listDir(skillsDir) {
			if !isDir(filepath.Join(skillsDir, d)) {
				continue
			}
			content, ok := readFileContent(filepath.Join(skillsDir, d, "SKILL.md"))
			if !ok {
				continue
			}
			for _, ref := range extractSiblingFileRefs(content) {
				siblingPath := filepath.Join(skillsDir, d, ref)
				if !isFile(siblingPath) {
					findings = append(findings, Finding{
						ID: "PD12", Severity: "error",
						Message: fmt.Sprintf(
							"%s/skills/%s/SKILL.md: references `%s` but the file does not exist in the skill directory",
							p.entryName, d, ref),
						Path: filepath.Join(p.sourceDir, "skills", d, "SKILL.md"),
					})
				}
			}
		}
	}
	return findings
}

func checkPD13(plugins []pluginInfo) []Finding {
	if len(plugins) == 0 {
		return nil // skip — PD4 failed
	}
	var findings []Finding
	for _, p := range plugins {
		skillsDir := filepath.Join(p.pluginDir, "skills")
		agentsDir := filepath.Join(p.pluginDir, "agents")
		for _, d := range listDir(skillsDir) {
			if !isDir(filepath.Join(skillsDir, d)) {
				continue
			}
			content, ok := readFileContent(filepath.Join(skillsDir, d, "SKILL.md"))
			if !ok {
				continue
			}
			for _, agentName := range extractAgentRefs(content) {
				agentPath := filepath.Join(agentsDir, agentName+".md")
				if !isFile(agentPath) {
					findings = append(findings, Finding{
						ID: "PD13", Severity: "error",
						Message: fmt.Sprintf(
							"%s/skills/%s/SKILL.md: references agent %q but agents/%s.md does not exist",
							p.entryName, d, agentName, agentName),
						Path: filepath.Join(p.sourceDir, "skills", d, "SKILL.md"),
					})
				}
			}
		}
	}
	return findings
}

func checkPD14(plugins []pluginInfo) []Finding {
	if len(plugins) == 0 {
		return nil // skip — PD4 failed
	}
	var findings []Finding
	for _, p := range plugins {
		skillsDir := filepath.Join(p.pluginDir, "skills")
		scriptDir := filepath.Join(p.pluginDir, "scripts")
		for _, d := range listDir(skillsDir) {
			if !isDir(filepath.Join(skillsDir, d)) {
				continue
			}
			content, ok := readFileContent(filepath.Join(skillsDir, d, "SKILL.md"))
			if !ok {
				continue
			}
			for _, scriptName := range extractScriptRefs(content) {
				scriptPath := filepath.Join(scriptDir, scriptName)
				if !isFile(scriptPath) {
					findings = append(findings, Finding{
						ID: "PD14", Severity: "warning",
						Message: fmt.Sprintf(
							"%s/skills/%s/SKILL.md: references script %q but scripts/%s does not exist",
							p.entryName, d, scriptName, scriptName),
						Path: filepath.Join(p.sourceDir, "skills", d, "SKILL.md"),
					})
				}
			}
		}
	}
	return findings
}

func checkPD15(plugins []pluginInfo) []Finding {
	if len(plugins) == 0 {
		return nil // skip — PD4 failed
	}
	var findings []Finding
	for _, p := range plugins {
		hooksPath := filepath.Join(p.pluginDir, "hooks", "hooks.json")
		if !isFile(hooksPath) {
			findings = append(findings, Finding{
				ID: "PD15", Severity: "error",
				Message: fmt.Sprintf("%s/hooks/hooks.json: file not found", p.entryName),
				Path:    filepath.Join(p.sourceDir, "hooks", "hooks.json"),
			})
			continue
		}
		content, ok := readFileContent(hooksPath)
		if !ok {
			findings = append(findings, Finding{
				ID: "PD15", Severity: "error",
				Message: fmt.Sprintf("%s/hooks/hooks.json: cannot read", p.entryName),
				Path:    filepath.Join(p.sourceDir, "hooks", "hooks.json"),
			})
			continue
		}
		var discard any
		if err := json.Unmarshal([]byte(content), &discard); err != nil {
			findings = append(findings, Finding{
				ID: "PD15", Severity: "error",
				Message: fmt.Sprintf("%s/hooks/hooks.json: invalid JSON — %s", p.entryName, err.Error()),
				Path:    filepath.Join(p.sourceDir, "hooks", "hooks.json"),
			})
		}
	}
	return findings
}

func checkPD16(plugins []pluginInfo) []Finding {
	if len(plugins) == 0 {
		return nil // skip — PD4 failed
	}
	var findings []Finding
	for _, p := range plugins {
		agentsDir := filepath.Join(p.pluginDir, "agents")
		for _, f := range listDir(agentsDir) {
			if !strings.HasSuffix(f, ".md") {
				continue
			}
			content, ok := readFileContent(filepath.Join(agentsDir, f))
			if !ok {
				findings = append(findings, Finding{
					ID: "PD16", Severity: "warning",
					Message: fmt.Sprintf("%s/agents/%s: cannot read", p.entryName, f),
					Path:    filepath.Join(p.sourceDir, "agents", f),
				})
				continue
			}
			meta, _, err := frontmatter.Parse([]byte(content))
			if err != nil {
				// Map any parse error to "missing YAML frontmatter".
				findings = append(findings, Finding{
					ID: "PD16", Severity: "warning",
					Message: fmt.Sprintf("%s/agents/%s: missing YAML frontmatter", p.entryName, f),
					Path:    filepath.Join(p.sourceDir, "agents", f),
				})
				continue
			}
			if !jsTruthy(meta["name"]) {
				findings = append(findings, Finding{
					ID: "PD16", Severity: "warning",
					Message: fmt.Sprintf("%s/agents/%s: frontmatter missing \"name\"", p.entryName, f),
					Path:    filepath.Join(p.sourceDir, "agents", f),
				})
			}
			if !jsTruthy(meta["description"]) {
				findings = append(findings, Finding{
					ID: "PD16", Severity: "warning",
					Message: fmt.Sprintf("%s/agents/%s: frontmatter missing \"description\"", p.entryName, f),
					Path:    filepath.Join(p.sourceDir, "agents", f),
				})
			}
			if !jsTruthy(meta["tools"]) {
				findings = append(findings, Finding{
					ID: "PD16", Severity: "warning",
					Message: fmt.Sprintf("%s/agents/%s: frontmatter missing \"tools\"", p.entryName, f),
					Path:    filepath.Join(p.sourceDir, "agents", f),
				})
			}
		}
	}
	return findings
}
