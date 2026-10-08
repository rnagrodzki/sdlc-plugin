package tools

import (
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/rnagrodzki/sdlc-plugin/internal/shipmeta"
)

// tipExceptions lists schema leaves deliberately exempt from
// TestTemplate_EveryLeafOptionHasTip, each with the reason it has no
// template tip and the review note recorded when the exception was added.
// Starts empty: every schema leaf currently has a live or commented tip in
// its template. Add an entry only with a reviewed reason — do not use this
// map to silence a tip that should simply be written.
var tipExceptions = map[string]struct {
	reason     string
	reviewNote string
}{}

// schemaLeaf is one scalar/array config option reachable from a schema's
// top-level properties, after resolving "$ref" and recursing through fixed
// object properties and additionalProperties-shaped maps.
type schemaLeaf struct {
	// path is a dotted diagnostic path, e.g. "commit.subjectPattern" or
	// "plan.guardrails.*.severity" (a "*" segment marks a dynamic
	// TOML-table-name map, such as a guardrail ID).
	path string
	// name is the bare property name used to build the template line
	// regex — the same name reappears verbatim as a TOML key regardless
	// of how deep its container is nested.
	name   string
	schema *jsonschema.Schema
}

// derefSchema follows a compiled schema's Ref chain to the schema it
// actually points at. A property declared as {"$ref": "#/$defs/x"}
// compiles to a wrapper schema whose own Properties/AdditionalProperties
// are empty; the real shape lives on .Ref.
func derefSchema(s *jsonschema.Schema) *jsonschema.Schema {
	for s != nil && s.Ref != nil {
		s = s.Ref
	}
	return s
}

// collectSchemaLeaves walks sch (after $ref resolution) and returns every
// leaf option under it, in a deterministic (sorted) order.
//
// An object property with its own fixed properties is a sub-table — it
// gets a template header ("[section.sub]"), never its own "name =" line —
// so it is recursed into rather than collected.
//
// A property whose additionalProperties resolves to an object with its own
// properties is a dynamic map of named sub-tables (e.g.
// "plan.guardrails.<id>"); it is recursed into too, with a "*" wildcard
// path segment standing in for the user-chosen table name, because the
// fixed property names live one level down (on the map's value schema).
//
// Everything else — including a map whose values are scalars, such as
// workspace.branch.typeMap — is a leaf: the property needs its own
// "name = value" line (live or commented) somewhere in the template, and
// that one line is the whole example (the map's entries are not walked).
func collectSchemaLeaves(sch *jsonschema.Schema, path string) []schemaLeaf {
	sch = derefSchema(sch)
	if sch == nil {
		return nil
	}
	names := make([]string, 0, len(sch.Properties))
	for n := range sch.Properties {
		names = append(names, n)
	}
	sort.Strings(names)

	var out []schemaLeaf
	for _, name := range names {
		p := derefSchema(sch.Properties[name])
		if p == nil {
			continue
		}
		childPath := path + "." + name
		if len(p.Properties) > 0 {
			out = append(out, collectSchemaLeaves(p, childPath)...)
			continue
		}
		if ap, ok := p.AdditionalProperties.(*jsonschema.Schema); ok {
			if apRes := derefSchema(ap); apRes != nil && len(apRes.Properties) > 0 {
				out = append(out, collectSchemaLeaves(ap, childPath+".*")...)
				continue
			}
		}
		out = append(out, schemaLeaf{path: childPath, name: name, schema: p})
	}
	return out
}

// resolveSchemaSection walks a dotted TOML header path (e.g.
// ["plan", "guardrails", "test-coverage-required"], from a header line like
// "[plan.guardrails.test-coverage-required]") down root's compiled schema
// and returns the schema that governs plain "key = value" lines written
// directly under that header.
//
// A path segment that is not a fixed property of the current schema, but
// the current schema is a dynamic map (its additionalProperties resolves
// to a schema), is treated as the map's key — e.g. the guardrail's
// TOML-table ID — and the map's value schema (e.g. guardrailItem) becomes
// current for the rest of the walk.
func resolveSchemaSection(root *jsonschema.Schema, path []string) *jsonschema.Schema {
	cur := root
	for _, seg := range path {
		cur = derefSchema(cur)
		if cur == nil {
			return nil
		}
		if next, ok := cur.Properties[seg]; ok {
			cur = next
			continue
		}
		if ap, ok := cur.AdditionalProperties.(*jsonschema.Schema); ok {
			cur = ap
			continue
		}
		return nil
	}
	return derefSchema(cur)
}

// compileTemplateSchema compiles the named schema file (relative to the
// repo root) for use by both tests in this file.
func compileTemplateSchema(t *testing.T, relPath string) *jsonschema.Schema {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join("..", "..", relPath))
	if err != nil {
		t.Fatalf("abs path for %s: %v", relPath, err)
	}
	sch, err := jsonschema.NewCompiler().Compile(abs)
	if err != nil {
		t.Fatalf("compile %s: %v", relPath, err)
	}
	return sch
}

// templateTipCases pairs each schema with the template it governs. Shared
// by both tests in this file.
func templateTipCases() []struct {
	name     string
	schema   string
	template string
} {
	return []struct {
		name     string
		schema   string
		template string
	}{
		{"config", "plugins/sdlc/schemas/sdlc-config.schema.json", configTemplate},
		{"local", "plugins/sdlc/schemas/sdlc-local.schema.json", localTemplate},
	}
}

// TestTemplate_EveryLeafOptionHasTip walks each schema (it follows local
// "#/$defs/..." refs), collects the leaf property names of each section,
// and checks the matching template with regexp `(?m)^\s*#?\s*<name>\s*=`.
// A leaf with no such line anywhere in its template — not even as a
// commented example — fails, unless it is listed in tipExceptions with a
// reason and a review note. A new schema option without a tip fails this
// test, which is the point: it forces the option to be documented (even if
// only as a commented-out example) before it ships.
func TestTemplate_EveryLeafOptionHasTip(t *testing.T) {
	for _, tc := range templateTipCases() {
		t.Run(tc.name, func(t *testing.T) {
			sch := compileTemplateSchema(t, tc.schema)
			topNames := make([]string, 0, len(sch.Properties))
			for n := range sch.Properties {
				topNames = append(topNames, n)
			}
			sort.Strings(topNames)

			checkedAny := false
			for _, top := range topNames {
				for _, lf := range collectSchemaLeaves(sch.Properties[top], top) {
					checkedAny = true
					if exc, exempt := tipExceptions[lf.path]; exempt {
						if exc.reason == "" || exc.reviewNote == "" {
							t.Errorf("tipExceptions[%q] needs both a reason and a reviewNote", lf.path)
						}
						continue
					}
					re := regexp.MustCompile(`(?m)^\s*#?\s*` + regexp.QuoteMeta(lf.name) + `\s*=`)
					if !re.MatchString(tc.template) {
						t.Errorf("%s: no live or commented %q line anywhere in the template for schema leaf %s", tc.name, lf.name+" = ...", lf.path)
					}
				}
			}
			if !checkedAny {
				t.Fatalf("%s: walked zero schema leaves; the schema file or the walk is broken", tc.name)
			}
		})
	}
}

// templateHeaderRe matches a live or commented TOML table header line,
// e.g. "[plan.guardrails.my-rule]" or "# [automation]".
var templateHeaderRe = regexp.MustCompile(`^\s*#?\s*\[([A-Za-z0-9_.-]+)\]\s*$`)

// templateKVRe matches a live or commented "name = value..." line. It does
// not require the value to be well-formed TOML — that is checked
// separately by attempting to decode the line.
var templateKVRe = regexp.MustCompile(`^\s*#?\s*([A-Za-z_][A-Za-z0-9_]*)\s*=\s*.+$`)

// TestTemplate_TipExamplesMatchSchema reads each live "<name> = <value>"
// line and each commented "# <name> = <value>" line of each template,
// decodes the line with toml.Unmarshal, and validates the value against
// the schema property of <name> in its enclosing section — tracked by the
// nearest preceding live or commented "[section]" header line above it.
//
// A line that does not decode as a self-contained TOML key/value (prose, a
// multi-line triple-quoted string's opening or continuation line) is not
// an example and is skipped. A line whose key is not a fixed property of
// its enclosing section is also skipped: it is either prose that happens
// to look like "Word = text", or a dynamic map entry (such as
// "[automation.steps]"'s per-step overrides) validated by the map's own
// value schema instead. An empty-string value is always skipped: both
// templates use "" as the shipped convention for "not configured yet",
// which does not need to satisfy a configured value's enum or pattern.
func TestTemplate_TipExamplesMatchSchema(t *testing.T) {
	for _, tc := range templateTipCases() {
		t.Run(tc.name, func(t *testing.T) {
			root := compileTemplateSchema(t, tc.schema)

			var section *jsonschema.Schema
			var sectionPath string
			checked := 0
			for _, line := range strings.Split(tc.template, "\n") {
				line = strings.TrimRight(line, "\r")

				if m := templateHeaderRe.FindStringSubmatch(line); m != nil {
					sectionPath = m[1]
					section = resolveSchemaSection(root, strings.Split(sectionPath, "."))
					continue
				}

				m := templateKVRe.FindStringSubmatch(line)
				if m == nil || section == nil {
					continue
				}
				key := m[1]

				propSchema := derefSchema(section.Properties[key])
				if propSchema == nil {
					// Not a fixed property of this section: either prose,
					// or a dynamic map entry validated against the map's
					// own value schema instead (e.g. automation.steps'
					// per-step "execute = \"confirm\"" overrides).
					if ap, ok := section.AdditionalProperties.(*jsonschema.Schema); ok {
						propSchema = derefSchema(ap)
					}
				}
				if propSchema == nil {
					continue
				}

				trimmed := strings.TrimSpace(line)
				trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, "#"))
				var holder map[string]any
				if err := toml.Unmarshal([]byte(trimmed), &holder); err != nil {
					continue // prose, or a multi-line string's opening/continuation line
				}
				val, ok := holder[key]
				if !ok {
					continue
				}
				if s, isStr := val.(string); isStr && s == "" {
					continue // the shipped "not configured yet" placeholder
				}

				checked++
				if err := propSchema.Validate(val); err != nil {
					t.Errorf("%s: [%s] %s = %v does not validate against its schema property: %v", tc.name, sectionPath, key, val, err)
				}
			}
			if checked == 0 {
				t.Fatalf("%s: validated zero example lines; the header/key regexes or the schema walk are broken", tc.name)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Task 11: ship.* ships as commented examples
// ---------------------------------------------------------------------------

// shipKeyLine returns the index and text, within lines (as returned by
// shipSectionLines), of the live or commented "key = value" line for key. It
// fails the test if no such line exists.
func shipKeyLine(t *testing.T, lines []string, key string) (int, string) {
	t.Helper()
	for i, l := range lines {
		if m := templateKVRe.FindStringSubmatch(l); m != nil && m[1] == key {
			return i, l
		}
	}
	t.Fatalf("no %q line found in the [ship] section", key)
	return -1, ""
}

// shipKeyHasTip reports whether lines[idx] (a ship key's example line) has a
// prose comment somewhere in the contiguous comment block directly above
// it — walking past any other key's own "key = value" example line on the
// way up (e.g. executeWaveInterval's line sits directly above
// executeWaveTimeout's, but is not itself a tip for executeWaveTimeout).
func shipKeyHasTip(lines []string, idx int) bool {
	isComment := func(l string) bool { return strings.HasPrefix(strings.TrimSpace(l), "#") }
	for i := idx - 1; i >= 0 && isComment(lines[i]); i-- {
		if templateKVRe.FindStringSubmatch(lines[i]) == nil {
			return true // a comment line that is not itself a key=value example
		}
	}
	return false
}

// decodeShipLineValue decodes a live or commented "key = value" line's value
// via toml.Unmarshal, the same trick TestTemplate_TipExamplesMatchSchema
// uses, so array/int/bool/string examples all compare by decoded shape
// rather than by source text.
func decodeShipLineValue(t *testing.T, line, key string) any {
	t.Helper()
	trimmed := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "#"))
	var holder map[string]any
	if err := toml.Unmarshal([]byte(trimmed), &holder); err != nil {
		t.Fatalf("decode %q: %v", line, err)
	}
	return holder[key]
}

// asDecodedTOML round-trips v through toml.Marshal/Unmarshal under key, so
// it decodes to the same shape (int64, []any, ...) decodeShipLineValue
// produces — letting reflect.DeepEqual compare a Go struct field against a
// template line's decoded value without hand-converting types on either
// side.
func asDecodedTOML(t *testing.T, key string, v any) any {
	t.Helper()
	b, err := toml.Marshal(map[string]any{key: v})
	if err != nil {
		t.Fatalf("marshal %s built-in default: %v", key, err)
	}
	var holder map[string]any
	if err := toml.Unmarshal(b, &holder); err != nil {
		t.Fatalf("round-trip decode %s built-in default: %v", key, err)
	}
	return holder[key]
}

// stepTable turns a list of step names into the on/off table form of
// ship.steps: every listed name maps to true.
func stepTable(names []string) map[string]any {
	table := make(map[string]any, len(names))
	for _, name := range names {
		table[name] = true
	}
	return table
}

// shipBuiltInDefaultsByKey pairs every ship.toml key except "quick" (which
// has no built-in default — see TestLocalTemplate_ShipQuickHasNoBuiltInDefault)
// with its shipmeta.ShipBuiltInDefaults field.
func shipBuiltInDefaultsByKey() map[string]any {
	d := shipmeta.ShipBuiltInDefaults
	return map[string]any{
		"auto":                        d.Auto,
		"bump":                        d.Bump,
		"draft":                       d.Draft,
		"rebase":                      d.Rebase,
		"reviewThreshold":             d.ReviewThreshold,
		"steps":                       stepTable(d.Steps),
		"executeWaveInterval":         d.ExecuteWaveInterval,
		"executeWaveTimeout":          d.ExecuteWaveTimeout,
		"verifyPipelineInterval":      d.VerifyPipelineInterval,
		"verifyPipelineMaxIterations": d.VerifyPipelineMaxIterations,
		"verifyPipelineTimeout":       d.VerifyPipelineTimeout,
		"awaitRemoteReviewers":        d.AwaitRemoteReviewers,
		"awaitRemoteReviewInterval":   d.AwaitRemoteReviewInterval,
		"awaitRemoteReviewTimeout":    d.AwaitRemoteReviewTimeout,
	}
}

// TestLocalTemplate_ShipSectionHasNoLiveKey verifies Task 11's core change:
// plugins/sdlc/templates/local.toml ships [ship] with its header live but
// not one live key under it, so shipmeta.ShipBuiltInDefaults applies to
// every new project rather than the template's old opinionated values.
func TestLocalTemplate_ShipSectionHasNoLiveKey(t *testing.T) {
	if !strings.Contains(localTemplate, "\n[ship]\n") {
		t.Fatal("localTemplate's [ship] header is missing or not live")
	}
	var decoded map[string]any
	if err := toml.Unmarshal([]byte(localTemplate), &decoded); err != nil {
		t.Fatalf("decode localTemplate: %v", err)
	}
	ship, ok := decoded["ship"].(map[string]any)
	if !ok {
		t.Fatal("localTemplate decodes with no [ship] table")
	}
	if len(ship) != 0 {
		t.Errorf("[ship] has %d live key(s): %v, want 0", len(ship), ship)
	}
}

// TestLocalTemplate_ShipCommentedDefaultsMatchBuiltIns verifies that every
// commented [ship] example — except "quick" — carries the exact value
// shipmeta.ShipBuiltInDefaults falls back to at runtime, so the template's
// example and the real behavior of an unconfigured project never diverge.
func TestLocalTemplate_ShipCommentedDefaultsMatchBuiltIns(t *testing.T) {
	lines := shipSectionLines(t)
	for key, want := range shipBuiltInDefaultsByKey() {
		t.Run(key, func(t *testing.T) {
			_, line := shipKeyLine(t, lines, key)
			if !strings.HasPrefix(strings.TrimSpace(line), "#") {
				t.Fatalf("%q is a live key, want a commented example: %q", key, line)
			}
			got := decodeShipLineValue(t, line, key)
			wantDecoded := asDecodedTOML(t, key, want)
			if !reflect.DeepEqual(got, wantDecoded) {
				t.Errorf("%s = %#v, want shipmeta.ShipBuiltInDefaults value %#v", key, got, wantDecoded)
			}
		})
	}
}

// TestLocalTemplate_ShipQuickHasNoBuiltInDefault verifies that "quick" stays
// a commented example (shipmeta.ShipBuiltInDefaults has no Quick field: an
// unset ship.quick leaves /ship --quick with no steps to run) and that its
// tip says so.
func TestLocalTemplate_ShipQuickHasNoBuiltInDefault(t *testing.T) {
	lines := shipSectionLines(t)
	idx, line := shipKeyLine(t, lines, "quick")
	if !strings.HasPrefix(strings.TrimSpace(line), "#") {
		t.Fatalf(`"quick" is a live key, want a commented example: %q`, line)
	}
	if idx == 0 || !strings.Contains(lines[idx-1], "No built-in default") {
		above := ""
		if idx > 0 {
			above = lines[idx-1]
		}
		t.Errorf("quick's tip does not say it has no built-in default; line directly above: %q", above)
	}
}

// TestLocalTemplate_ShipEveryKeyKeepsTip verifies that every one of the 15
// [ship] keys (14 with a built-in default plus "quick") still has a prose
// tip somewhere in the contiguous comment block above its example line —
// commenting out the value must not have swallowed its explanation.
func TestLocalTemplate_ShipEveryKeyKeepsTip(t *testing.T) {
	lines := shipSectionLines(t)
	keys := make([]string, 0, 15)
	for key := range shipBuiltInDefaultsByKey() {
		keys = append(keys, key)
	}
	keys = append(keys, "quick")
	sort.Strings(keys)
	for _, key := range keys {
		t.Run(key, func(t *testing.T) {
			idx, _ := shipKeyLine(t, lines, key)
			if !shipKeyHasTip(lines, idx) {
				t.Errorf("%q has no prose tip in the comment block above it", key)
			}
		})
	}
}
