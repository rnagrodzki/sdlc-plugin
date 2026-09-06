// Package dimensions reads and validates review-dimension Markdown files
// (`.sdlc/review-dimensions/<name>.md`). It is a Go port of
// scripts/lib/dimensions.js in the sdlc-utilities plugin: KNOWN_FIELDS,
// VALID_SEVERITIES, GUARDRAIL_SEVERITIES, the `_common.md` shared-prompt
// convention, and the D1-D13 field/severity validation checks
// (validateDimensionFile). It also ports scripts/lib/dimension-to-instructions.js,
// the deterministic dimension -> GitHub Copilot instructions-mirror
// transform (R-copilot-mirror).
//
// Frontmatter extraction and YAML decoding are delegated to
// internal/frontmatter, which replaces the source's hand-rolled
// parseSimpleYaml with gopkg.in/yaml.v3.
package dimensions

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/rnagrodzki/sdlc-plugin/internal/frontmatter"
)

// KnownFields lists the recognized review-dimension frontmatter fields, in
// the same order as the source KNOWN_FIELDS Set literal. The order matters:
// D11's "did you mean" suggestion list is built by scanning KnownFields in
// this order (a Go map would randomize it).
var KnownFields = []string{
	"name", "description", "triggers", "skip-when",
	"severity", "max-files", "requires-full-diff", "model",
}

// ValidSeverities is the canonical severity vocabulary for review
// dimensions (D6).
var ValidSeverities = []string{"critical", "high", "medium", "low", "info"}

// GuardrailSeverities is the canonical severity vocabulary for plan/execute
// guardrails (R17) — ported from the source's GUARDRAIL_SEVERITIES. It is
// unrelated to review-dimension frontmatter (guardrails are stored as JSON
// entries in .sdlc/config.json, not as Markdown files with YAML
// frontmatter), so it is not exercised by Validate; it is exported for
// downstream consumers that need the vocabulary constant.
var GuardrailSeverities = []string{"error", "warning"}

// commonPromptFile is the shared common-prompt filename (R-common-prompt,
// issue #519). It carries no YAML frontmatter and is excluded from Load's
// dimension listing.
const commonPromptFile = "_common.md"

// Dimension is a single parsed review-dimension Markdown file.
type Dimension struct {
	// File is the file's basename, e.g. "security.md".
	File string
	// Meta is the decoded YAML frontmatter. Nil when Err is set.
	Meta map[string]any
	// Body is the Markdown body following the frontmatter block, trimmed.
	Body string
	// Common is the trimmed content of the directory's _common.md, shared
	// across every Dimension loaded from the same directory. Empty when
	// _common.md is absent, empty, or unreadable.
	Common string
	// Err is set when the file could not be read, or when
	// frontmatter.Parse could not find/decode a frontmatter block. Validate
	// reports this as check D0 (read failure) or D1 (missing frontmatter).
	Err error
}

// Load reads every review-dimension Markdown file in dir (all "*.md" files
// except _common.md) and returns one Dimension per file, sorted by
// filename for deterministic output (the source's fs.readdirSync order is
// filesystem-dependent and not otherwise meaningful here).
//
// A file that cannot be read or parsed still produces a Dimension (with Err
// set) rather than being dropped or failing Load outright — this mirrors
// validateDimensionFile's per-file try/catch and lets Validate reproduce
// the D0/D1 error strings for that file.
//
// A missing dir is not an error: Load returns an empty slice, matching the
// source's resolveDimensionsDir/validateAll behavior where a nonexistent
// dimensions directory yields zero results rather than failing.
func Load(dir string) ([]Dimension, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil
	}

	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".md") || name == commonPromptFile {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)

	common := readCommonPrompt(dir)

	dims := make([]Dimension, 0, len(names))
	for _, name := range names {
		d := Dimension{File: name, Common: common}

		content, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			d.Err = err
			dims = append(dims, d)
			continue
		}

		meta, body, err := frontmatter.Parse(content)
		if err != nil {
			d.Err = err
			dims = append(dims, d)
			continue
		}
		d.Meta = meta
		d.Body = string(body)
		dims = append(dims, d)
	}

	return dims, nil
}

// readCommonPrompt reads and trims _common.md from dir. It returns "" when
// the file is absent, unreadable, or blank after trimming — mirroring the
// source's readCommonPrompt.
func readCommonPrompt(dir string) string {
	content, err := os.ReadFile(filepath.Join(dir, commonPromptFile))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(content))
}

// ---------------------------------------------------------------------------
// Validation (D1-D13)
// ---------------------------------------------------------------------------

var namePattern = regexp.MustCompile(`^[a-z0-9-]+$`)

// Validate reproduces validateDimensionFile's field/severity checks (D1-D13)
// for a single Dimension and returns the flattened error and warning
// messages (errors first, then warnings — the source keeps them as two
// separate lists; this signature has no room for that distinction, so
// callers that need to tell them apart should re-derive severity from the
// message's check code, e.g. by prefixing messages upstream if that
// distinction becomes necessary).
//
// D10 (cross-file duplicate dimension name) is intentionally out of scope:
// it requires comparing every Dimension in a directory against each other,
// which the single-Dimension Validate(d Dimension) signature in the
// Contract cannot express. Callers that need D10 must implement it
// themselves over the []Dimension slice Load returns (e.g. by tracking
// first-seen file per name, as validateAll does).
func Validate(d Dimension) []string {
	if d.Err != nil {
		if errors.Is(d.Err, frontmatter.ErrNoFrontmatter) {
			// D1 — Frontmatter present
			return []string{"Missing YAML frontmatter block (--- delimiters)"}
		}
		// D0 — file could not be read. The source's message embeds the
		// underlying fs error text verbatim; Go's os error text differs
		// from Node's, so byte-for-byte parity with the JS message isn't
		// achievable here — only the check's shape is preserved.
		return []string{fmt.Sprintf("Cannot read file: %s", d.Err.Error())}
	}

	var errs []string
	var warnings []string
	fm := d.Meta
	if fm == nil {
		fm = map[string]any{}
	}

	// D11 — Unknown fields (checked before required-field checks so typo
	// suggestions appear early, matching the source's ordering). Keys are
	// sorted for determinism: frontmatter.Parse returns map[string]any,
	// which does not preserve YAML source order the way the JS Object
	// built by parseSimpleYaml did.
	keys := make([]string, 0, len(fm))
	for k := range fm {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if knownField(key) {
			continue
		}
		var suggestions []string
		for _, f := range KnownFields {
			if levenshtein(f, key) <= 2 {
				suggestions = append(suggestions, f)
			}
		}
		hint := ""
		if len(suggestions) > 0 {
			hint = fmt.Sprintf(" (did you mean: %s?)", strings.Join(suggestions, ", "))
		}
		warnings = append(warnings, fmt.Sprintf("Unknown frontmatter field: %q%s", key, hint))
	}

	// D2 — name
	nameVal := fm["name"]
	if jsFalsy(nameVal) && !isZeroInt(nameVal) {
		errs = append(errs, "Missing required field: name")
	} else if name, ok := nameVal.(string); !ok {
		errs = append(errs, "Field \"name\" must be a string")
	} else if !namePattern.MatchString(name) {
		errs = append(errs, fmt.Sprintf("Field \"name\" must be lowercase letters, digits, and hyphens only (got: %q)", name))
	} else if len(name) > 64 {
		errs = append(errs, fmt.Sprintf("Field \"name\" exceeds 64 characters (got: %d)", len(name)))
	}

	// D3 — description
	descVal := fm["description"]
	if jsFalsy(descVal) {
		errs = append(errs, "Missing required field: description")
	} else if desc, ok := descVal.(string); !ok {
		errs = append(errs, "Field \"description\" must be a string")
	} else if strings.TrimSpace(desc) == "" {
		errs = append(errs, "Field \"description\" must not be empty")
	} else if len(desc) > 256 {
		errs = append(errs, fmt.Sprintf("Field \"description\" exceeds 256 characters (got: %d)", len(desc)))
	}

	// D4/D5 — triggers
	triggersVal := fm["triggers"]
	if jsFalsy(triggersVal) {
		errs = append(errs, "Missing required field: triggers (must be a non-empty array of glob patterns)")
	} else if triggers, ok := triggersVal.([]any); !ok {
		errs = append(errs, "Field \"triggers\" must be an array of strings")
	} else if len(triggers) == 0 {
		errs = append(errs, "Field \"triggers\" must contain at least one pattern")
	} else {
		for _, pattern := range triggers {
			if !isValidGlob(pattern) {
				errs = append(errs, fmt.Sprintf("Invalid glob pattern in triggers: %q", fmt.Sprint(pattern)))
			}
		}
	}

	// D6 — severity (optional)
	if severityVal, present := fm["severity"]; present {
		s, ok := severityVal.(string)
		if !ok || !contains(ValidSeverities, s) {
			warnings = append(warnings, fmt.Sprintf(
				"Field \"severity\" must be one of: critical, high, medium, low, info (got: %q)", fmt.Sprint(severityVal)))
		}
	}

	// D7 — max-files (optional)
	if mfVal, present := fm["max-files"]; present {
		mf, ok := mfVal.(int)
		if !ok || mf <= 0 {
			warnings = append(warnings, fmt.Sprintf("Field \"max-files\" must be a positive integer (got: %s)", jsonStringify(mfVal)))
		}
	}

	// D8 — skip-when (optional)
	if swVal, present := fm["skip-when"]; present {
		skipWhen, ok := swVal.([]any)
		if !ok {
			warnings = append(warnings, "Field \"skip-when\" must be an array of strings")
		} else {
			for _, pattern := range skipWhen {
				if !isValidGlob(pattern) {
					warnings = append(warnings, fmt.Sprintf("Invalid glob pattern in skip-when: %q", fmt.Sprint(pattern)))
				}
			}
		}
	}

	// D9 — body non-empty
	if len(d.Body) < 10 {
		errs = append(errs, fmt.Sprintf("Body must contain at least 10 characters of review instructions (got: %d)", len(d.Body)))
	}

	// D12 — requires-full-diff (optional)
	if rfdVal, present := fm["requires-full-diff"]; present {
		if _, ok := rfdVal.(bool); !ok {
			warnings = append(warnings, fmt.Sprintf("Field \"requires-full-diff\" must be a boolean (got: %s)", jsonStringify(rfdVal)))
		}
	}

	// D13 — model (optional)
	if modelVal, present := fm["model"]; present {
		s, ok := modelVal.(string)
		if !ok || strings.TrimSpace(s) == "" {
			warnings = append(warnings, fmt.Sprintf("Field \"model\" must be a non-empty string (got: %s)", jsonStringify(modelVal)))
		}
	}

	return append(errs, warnings...)
}

func knownField(key string) bool {
	return contains(KnownFields, key)
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// jsonStringify renders v the way JavaScript's JSON.stringify would for the
// scalar/array shapes frontmatter values take on (string, number, bool,
// nil, []any) — used to reproduce the "(got: ...)" fragments in D7/D12/D13
// that the source builds with JSON.stringify.
func jsonStringify(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(b)
}

// jsFalsy reports whether v would be falsy under JavaScript semantics:
// undefined/null, false, 0, "", or NaN. YAML decoding never produces NaN,
// and arrays/maps are always truthy in JS (even empty ones), so those cases
// are omitted.
func jsFalsy(v any) bool {
	if v == nil {
		return true
	}
	switch x := v.(type) {
	case bool:
		return !x
	case string:
		return x == ""
	case int:
		return x == 0
	case int64:
		return x == 0
	case float64:
		return x == 0
	}
	return false
}

func isZeroInt(v any) bool {
	switch x := v.(type) {
	case int:
		return x == 0
	case int64:
		return x == 0
	}
	return false
}

// isValidGlob ports isValidGlob: a lightweight, non-minimatch syntax check
// (non-empty, no run of 3+ "*", balanced "[" / "]").
func isValidGlob(pattern any) bool {
	s, ok := pattern.(string)
	if !ok {
		return false
	}
	if strings.TrimSpace(s) == "" {
		return false
	}
	if strings.Contains(s, "***") {
		return false
	}
	depth := 0
	for _, ch := range s {
		switch ch {
		case '[':
			depth++
		case ']':
			depth--
			if depth < 0 {
				return false
			}
		}
	}
	return depth == 0
}

// levenshtein ports the source's dynamic-programming edit-distance
// implementation, used for D11 "did you mean" suggestions.
func levenshtein(a, b string) int {
	ar, br := []rune(a), []rune(b)
	m, n := len(ar), len(br)
	dp := make([][]int, m+1)
	for i := range dp {
		dp[i] = make([]int, n+1)
		dp[i][0] = i
	}
	for j := 0; j <= n; j++ {
		dp[0][j] = j
	}
	for i := 1; i <= m; i++ {
		for j := 1; j <= n; j++ {
			if ar[i-1] == br[j-1] {
				dp[i][j] = dp[i-1][j-1]
			} else {
				dp[i][j] = 1 + min3(dp[i-1][j], dp[i][j-1], dp[i-1][j-1])
			}
		}
	}
	return dp[m][n]
}

func min3(a, b, c int) int {
	m := a
	if b < m {
		m = b
	}
	if c < m {
		m = c
	}
	return m
}

// ---------------------------------------------------------------------------
// ToInstructions (R-copilot-mirror)
// ---------------------------------------------------------------------------

// copilotCheckboxRe strips a Markdown checkbox prefix ("- [ ] ", "- [x] ",
// "- [X] ") down to a plain "- " list item, matching the source's
// /^(\s*)-\s+\[[ xX]\]\s+/gm replacement.
var copilotCheckboxRe = regexp.MustCompile(`(?m)^(\s*)-\s+\[[ xX]\]\s+`)

// ToInstructions transforms a Dimension into its GitHub Copilot
// instructions-mirror Markdown (`.github/instructions/<name>.instructions.md`
// content), porting dimensionToInstructions. Unlike the source, which
// throws when required fields are absent, ToInstructions returns "" when d
// lacks a usable name or a non-empty triggers list — this signature has no
// error return, so an empty string is the "cannot render" signal callers
// must check for.
func ToInstructions(d Dimension) string {
	fm := d.Meta
	if fm == nil {
		fm = map[string]any{}
	}

	name, _ := fm["name"].(string)
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}

	triggersVal, _ := fm["triggers"].([]any)
	var triggers []string
	for _, t := range triggersVal {
		if s, ok := t.(string); ok {
			triggers = append(triggers, s)
		}
	}
	if len(triggers) == 0 {
		return ""
	}
	applyTo := strings.Join(triggers, ",")

	description, _ := fm["description"].(string)
	description = strings.TrimSpace(description)

	severity, _ := fm["severity"].(string)
	severity = strings.TrimSpace(severity)
	if severity == "" {
		severity = "medium"
	}

	var skipWhen []string
	if swVal, ok := fm["skip-when"].([]any); ok {
		for _, s := range swVal {
			if str, ok := s.(string); ok {
				skipWhen = append(skipWhen, str)
			}
		}
	}

	var out []string
	out = append(out, "---")
	out = append(out, fmt.Sprintf("applyTo: %q", applyTo))
	out = append(out, "---")
	out = append(out, fmt.Sprintf("# %s — Review Instructions", name))
	out = append(out, "")
	if description != "" {
		out = append(out, description)
		out = append(out, "")
	}
	out = append(out, fmt.Sprintf("Default severity: %s", severity))

	if common := strings.TrimSpace(d.Common); common != "" {
		out = append(out, "")
		out = append(out, "## Common Review Instructions")
		out = append(out, "")
		out = append(out, common)
	}

	if checklist := extractSection(d.Body, "Checklist"); checklist != "" {
		out = append(out, "")
		out = append(out, "## Checklist")
		out = append(out, "")
		out = append(out, copilotCheckboxRe.ReplaceAllString(checklist, "$1- "))
	}

	if severityGuide := extractSection(d.Body, "Severity Guide"); severityGuide != "" {
		out = append(out, "")
		out = append(out, "## Severity Guide")
		out = append(out, "")
		out = append(out, severityGuide)
	}

	if len(skipWhen) > 0 {
		out = append(out, "")
		out = append(out, "## Note")
		out = append(out, "")
		out = append(out, fmt.Sprintf(
			"In Claude Code reviews, files matching these patterns are excluded: %s.", strings.Join(skipWhen, ", ")))
		out = append(out, "Copilot path-specific instructions do not support exclusion patterns — use judgment when findings apply to these files.")
	}

	return strings.Join(out, "\n") + "\n"
}

// extractSection extracts the inner content (without the heading line
// itself) of a single "## <heading>" section from body. The section runs
// until the next "## " heading at the same level, or EOF. Returns "" when
// the section is absent.
func extractSection(body, heading string) string {
	lines := strings.Split(body, "\n")
	startRe := regexp.MustCompile(`^##\s+` + regexp.QuoteMeta(heading) + `\s*$`)
	start := -1
	for i, l := range lines {
		if startRe.MatchString(l) {
			start = i + 1
			break
		}
	}
	if start == -1 {
		return ""
	}
	end := len(lines)
	headingLineRe := regexp.MustCompile(`^##\s+`)
	for i := start; i < len(lines); i++ {
		if headingLineRe.MatchString(lines[i]) {
			end = i
			break
		}
	}
	return strings.TrimSpace(strings.Join(lines[start:end], "\n"))
}
