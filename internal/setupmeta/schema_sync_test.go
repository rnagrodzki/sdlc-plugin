package setupmeta

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// optionExceptions documents a Field whose Options are known not to be
// fully accepted by its schema node, together with the reason that makes
// the gap intentional rather than a bug. Keyed by "<section ID>.<field
// name>". Add an entry here only after confirming the schema is correct as
// written — otherwise fix the schema instead.
var optionExceptions = map[string]string{}

// rawSchema is a JSON Schema document decoded generically (map[string]any),
// so TestFieldOptions_AcceptedBySchema can walk "properties" and "$defs" for
// every section through its Section.ConfigFile/ConfigPath, instead of
// decoding one hand-written Go struct per section (the approach the
// narrower, single-field tests above this file use).
type rawSchema map[string]interface{}

// loadRawSchema reads and parses one schema file under
// plugins/sdlc/schemas/.
func loadRawSchema(t *testing.T, fileName string) rawSchema {
	t.Helper()
	path := filepath.Join("..", "..", "plugins", "sdlc", "schemas", fileName)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read schema %s: %v", path, err)
	}
	var doc rawSchema
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse schema %s: %v", path, err)
	}
	return doc
}

// asObject type-asserts v to a JSON object, returning nil on any mismatch
// (missing key, null, or a non-object value) instead of panicking.
func asObject(v interface{}) map[string]interface{} {
	m, _ := v.(map[string]interface{})
	return m
}

// resolveRef follows one "$ref": "#/$defs/Name" pointer on node, if present;
// otherwise it returns node unchanged. A "$ref" to anything other than a
// local "#/$defs/..." entry resolves to nil.
func resolveRef(doc rawSchema, node map[string]interface{}) map[string]interface{} {
	if node == nil {
		return nil
	}
	ref, ok := node["$ref"].(string)
	if !ok {
		return node
	}
	const prefix = "#/$defs/"
	if !strings.HasPrefix(ref, prefix) {
		return nil
	}
	defs := asObject(doc["$defs"])
	return asObject(defs[strings.TrimPrefix(ref, prefix)])
}

// schemaNodeFromNode walks start's "properties" by each dot-separated
// segment of path, resolving a "$ref" before every step (including the
// last), and returns the node it lands on, or nil when any segment is
// missing. It is used twice: once from the schema document root with
// Section.ConfigPath to find a section's node, and once from that section
// node with Field.Name to find a field's node.
func schemaNodeFromNode(doc rawSchema, start map[string]interface{}, path string) map[string]interface{} {
	node := start
	for _, seg := range strings.Split(path, ".") {
		node = resolveRef(doc, node)
		if node == nil {
			return nil
		}
		node = asObject(asObject(node["properties"])[seg])
	}
	return resolveRef(doc, node)
}

// schemaFileForConfigFile maps a Section.ConfigFile value to the schema
// file that validates it. Sections with any other ConfigFile value (the
// delegated/content sections) carry no Fields and never reach this lookup.
func schemaFileForConfigFile(configFile string) string {
	switch configFile {
	case ".sdlc-v2/config.toml":
		return "sdlc-config.schema.json"
	case ".sdlc-v2/local.toml":
		return "sdlc-local.schema.json"
	default:
		return ""
	}
}

// collectSchemaEnum returns every string value a schema node accepts via
// "enum" or a single-value "const", unioned across "oneOf"/"anyOf"
// alternatives, or (for an array-typed node) read from its "items" schema,
// or (for an object-typed node, such as a flag table) read from its
// "propertyNames" schema.
// Returns nil when the node declares no enum at all — e.g. ship.bump
// validates by "pattern" instead, by design (see ShipFields' bump
// Description) — such a field is out of this test's scope.
func collectSchemaEnum(doc rawSchema, node map[string]interface{}) []string {
	node = resolveRef(doc, node)
	if node == nil {
		return nil
	}
	if c, ok := node["const"].(string); ok {
		return []string{c}
	}
	if enumRaw, ok := node["enum"].([]interface{}); ok {
		values := make([]string, 0, len(enumRaw))
		for _, e := range enumRaw {
			if s, ok := e.(string); ok {
				values = append(values, s)
			}
		}
		return values
	}
	for _, key := range []string{"oneOf", "anyOf"} {
		alts, ok := node[key].([]interface{})
		if !ok {
			continue
		}
		var values []string
		for _, altRaw := range alts {
			values = append(values, collectSchemaEnum(doc, asObject(altRaw))...)
		}
		if len(values) > 0 {
			return values
		}
	}
	if items := asObject(node["items"]); items != nil {
		return collectSchemaEnum(doc, items)
	}
	if names := asObject(node["propertyNames"]); names != nil {
		return collectSchemaEnum(doc, names)
	}
	return nil
}

// optionsMissingFromSchema returns the Options of f that the schema node
// of f does not accept. A flag-set field whose node has no enum returns
// every option, so the caller reports it. Any other field type whose node
// has no enum returns nil: the schema validates it some other way (e.g. by
// "pattern") and there is nothing to compare against.
func optionsMissingFromSchema(doc rawSchema, sectionNode map[string]interface{}, f Field) []string {
	enumValues := collectSchemaEnum(doc, schemaNodeFromNode(doc, sectionNode, f.Name))
	if len(enumValues) == 0 {
		if f.Type == "flag-set" {
			return slices.Clone(f.Options)
		}
		return nil
	}
	var missing []string
	for _, opt := range f.Options {
		if !slices.Contains(enumValues, opt) {
			missing = append(missing, opt)
		}
	}
	return missing
}

// TestFieldOptions_AcceptedBySchema finds the schema of each section through
// Section.ConfigFile and Section.ConfigPath, then checks that every value in
// each "enum", "multi-enum" or "flag-set" Field's Options is accepted by the
// matching schema property (found through Field.Name, which may be dotted,
// e.g. "versionFile.fileType"). A flag-set field is read through the
// "propertyNames" enum of its schema table. An "enum" or "multi-enum" field
// whose schema node declares no enum at all (e.g. one validated by "pattern")
// is out of scope and skipped. A "flag-set" field with no schema enum is an
// error, not a skip. A genuine mismatch fails the test unless listed in
// optionExceptions with a reason.
func TestFieldOptions_AcceptedBySchema(t *testing.T) {
	schemas := map[string]rawSchema{}

	for _, section := range Sections() {
		if len(section.Fields) == 0 {
			continue
		}
		schemaFile := schemaFileForConfigFile(section.ConfigFile)
		if schemaFile == "" {
			continue
		}
		doc, ok := schemas[schemaFile]
		if !ok {
			doc = loadRawSchema(t, schemaFile)
			schemas[schemaFile] = doc
		}

		root := map[string]interface{}{"properties": doc["properties"]}
		sectionNode := schemaNodeFromNode(doc, root, section.ConfigPath)
		if sectionNode == nil {
			if reason, ok := optionExceptions[section.ID]; ok {
				t.Logf("%s: no schema node at ConfigPath %q in %s, exempted (%s)", section.ID, section.ConfigPath, schemaFile, reason)
				continue
			}
			t.Errorf("%s: no schema node at ConfigPath %q in %s", section.ID, section.ConfigPath, schemaFile)
			continue
		}

		for _, f := range section.Fields {
			if f.Type != "enum" && f.Type != "multi-enum" && f.Type != "flag-set" {
				continue
			}
			if len(f.Options) == 0 {
				continue
			}
			label := section.ID + "." + f.Name

			fieldNode := schemaNodeFromNode(doc, sectionNode, f.Name)
			if fieldNode == nil {
				if reason, ok := optionExceptions[label]; ok {
					t.Logf("%s: no schema property at %q, exempted (%s)", label, f.Name, reason)
					continue
				}
				t.Errorf("%s: schema has no property at %q under ConfigPath %q (%s)", label, f.Name, section.ConfigPath, schemaFile)
				continue
			}

			enumValues := collectSchemaEnum(doc, fieldNode)
			missing := optionsMissingFromSchema(doc, sectionNode, f)
			if len(missing) == 0 {
				continue
			}
			if reason, ok := optionExceptions[label]; ok {
				t.Logf("%s: schema enum %v rejects Options %v, exempted (%s)", label, enumValues, missing, reason)
				continue
			}
			t.Errorf("%s: schema enum %v does not accept Options %v (ConfigFile=%s ConfigPath=%s)", label, enumValues, missing, section.ConfigFile, section.ConfigPath)
		}
	}
}

// shipSectionNode returns the real "ship" section node of
// sdlc-local.schema.json, for the negative tests below.
func shipSectionNode(t *testing.T) (rawSchema, map[string]interface{}) {
	t.Helper()
	doc := loadRawSchema(t, "sdlc-local.schema.json")
	root := map[string]interface{}{"properties": doc["properties"]}
	node := schemaNodeFromNode(doc, root, "ship")
	if node == nil {
		t.Fatal("schema has no property at ship")
	}
	return doc, node
}

// TestOptionsMissingFromSchema_FlagSetUnknownOption proves that a flag-set
// option the schema propertyNames enum does not list is found. The fake
// "steps" field below has one real step and one name the schema rejects.
func TestOptionsMissingFromSchema_FlagSetUnknownOption(t *testing.T) {
	doc, shipNode := shipSectionNode(t)
	f := Field{Name: "steps", Type: "flag-set", Options: []string{"execute", "bogus"}}

	got := optionsMissingFromSchema(doc, shipNode, f)
	if want := []string{"bogus"}; !slices.Equal(got, want) {
		t.Errorf("optionsMissingFromSchema = %v, want %v", got, want)
	}
}

// TestOptionsMissingFromSchema_FlagSetWithoutEnum proves that a flag-set
// field whose schema node has no enum reports every option instead of being
// skipped. ship.auto is a boolean, so it has no propertyNames enum.
func TestOptionsMissingFromSchema_FlagSetWithoutEnum(t *testing.T) {
	doc, shipNode := shipSectionNode(t)
	f := Field{Name: "auto", Type: "flag-set", Options: []string{"execute", "commit"}}

	got := optionsMissingFromSchema(doc, shipNode, f)
	if !slices.Equal(got, f.Options) {
		t.Errorf("optionsMissingFromSchema = %v, want every option %v", got, f.Options)
	}
}

// TestReviewMaxParallelDimensionsSchemaMatchesField pins that the
// review.maxParallelDimensions setup field and its JSON schema property stay
// in sync on type, minimum, and default. TestFieldOptions_AcceptedBySchema
// above only compares enum/multi-enum fields against the schema, so a "number"
// field like this one needs its own guard against drift.
func TestReviewMaxParallelDimensionsSchemaMatchesField(t *testing.T) {
	var field *Field
	for _, section := range Sections() {
		if section.ID != "review" {
			continue
		}
		for i := range section.Fields {
			if section.Fields[i].Name == "maxParallelDimensions" {
				field = &section.Fields[i]
			}
		}
	}
	if field == nil {
		t.Fatal("review section has no maxParallelDimensions field")
	}
	if field.Type != "number" {
		t.Errorf("field.Type = %q, want %q", field.Type, "number")
	}
	if field.Default != 8 {
		t.Errorf("field.Default = %v, want 8", field.Default)
	}
	if field.Min == nil || *field.Min != 1 {
		t.Errorf("field.Min = %v, want 1", field.Min)
	}

	doc := loadRawSchema(t, "sdlc-local.schema.json")
	root := map[string]interface{}{"properties": doc["properties"]}
	node := schemaNodeFromNode(doc, root, "review.maxParallelDimensions")
	if node == nil {
		t.Fatal("schema has no property at review.maxParallelDimensions")
	}
	if node["type"] != "integer" {
		t.Errorf("schema type = %v, want %q", node["type"], "integer")
	}
	if minimum, ok := node["minimum"].(float64); !ok || minimum != 1 {
		t.Errorf("schema minimum = %v, want 1", node["minimum"])
	}
	if def, ok := node["default"].(float64); !ok || def != 8 {
		t.Errorf("schema default = %v, want 8", node["default"])
	}
}

// uncoveredSchemaKeys lists each schema leaf key that has no form field and
// is not below a delegated section. Key: "<config|local>:<dotted path>".
// Value: one sentence that tells where the user sets the key.
var uncoveredSchemaKeys = map[string]string{
	"config:execute.baseSync":                         "Set it by hand in .sdlc-v2/config.toml.",
	"config:execute.commitWaves":                      "Set it by hand in .sdlc-v2/config.toml.",
	"config:git.baseBranch":                           "Set it by hand in .sdlc-v2/config.toml; other flows also write it from the detected default branch.",
	"config:harden.instructions.copilot-instructions": "Edit the list by hand in the [harden.instructions] table of .sdlc-v2/config.toml.",
	"config:harden.instructions.execute-guardrails":   "Edit the list by hand in the [harden.instructions] table of .sdlc-v2/config.toml.",
	"config:harden.instructions.plan-guardrails":      "Edit the list by hand in the [harden.instructions] table of .sdlc-v2/config.toml.",
	"config:harden.instructions.review-dimensions":    "Edit the list by hand in the [harden.instructions] table of .sdlc-v2/config.toml.",
	"config:instructions":                             "Edit the per-step lists by hand in the [instructions] table of .sdlc-v2/config.toml.",
	"config:jira.projects":                            "Set the list of project keys by hand in .sdlc-v2/config.toml; the Jira form shows only jira.defaultProject.",
	"local:automation.reviewFixIterations":            "Set it by hand in .sdlc-v2/local.toml or ~/.sdlc/local.toml.",
	"local:automation.reviewFixSeverityThreshold":     "Set it by hand in .sdlc-v2/local.toml or ~/.sdlc/local.toml.",
	"local:automation.steps":                          "Set the table of per-step modes by hand in .sdlc-v2/local.toml or ~/.sdlc/local.toml.",
	"local:dashboard.autoStart":                       "Set it by hand in .sdlc-v2/local.toml or ~/.sdlc/local.toml; /dashboard does not write it.",
	"local:dashboard.port":                            "Set it by hand in .sdlc-v2/local.toml or ~/.sdlc/local.toml; /dashboard does not write it.",
	"local:executePrefs.auto":                         "Set it in .sdlc-v2/local.toml or ~/.sdlc/local.toml, where /execute saves the preference when the user asks.",
	"local:executePrefs.highRiskAutoApprove":          "Set it in .sdlc-v2/local.toml or ~/.sdlc/local.toml, where /execute saves the preference when the user asks.",
	"local:executePrefs.quality":                      "Set it in .sdlc-v2/local.toml or ~/.sdlc/local.toml, where /execute saves the preference when the user asks.",
	"local:instructions":                              "Edit the per-step lists by hand in the [instructions] table of .sdlc-v2/local.toml or ~/.sdlc/local.toml.",
	"local:planStyle.audience":                        "Do not set it; the deprecated key moved to [style], which the communication-style section owns.",
	"local:planStyle.language":                        "Do not set it; the deprecated key moved to [style], which the communication-style section owns.",
	"local:planStyle.technicalTerms":                  "Do not set it; the deprecated key moved to [style], which the communication-style section owns.",
	"local:planStyle.tone":                            "Do not set it; the deprecated key moved to [style], which the communication-style section owns.",
	"local:planStyle.verbosity":                       "Do not set it; the deprecated key is unused and visualDensity in [style] replaces it.",
	"local:planStyle.writingStandard":                 "Do not set it; the deprecated key moved to [style], which the communication-style section owns.",
	"local:version":                                   "Do not set it; the tools write the schema version marker in .sdlc-v2/local.toml.",
	"local:workspace.branch.slugMaxLength":            "Set it by hand in .sdlc-v2/local.toml or ~/.sdlc/local.toml.",
	"local:workspace.branch.template":                 "Set it by hand in .sdlc-v2/local.toml or ~/.sdlc/local.toml.",
	"local:workspace.branch.typeMap":                  "Set the table of type to prefix by hand in .sdlc-v2/local.toml or ~/.sdlc/local.toml.",
}

// schemaKeyKind maps a schema file name to the key prefix used in
// uncoveredSchemaKeys: "config" or "local".
func schemaKeyKind(schemaFile string) string {
	switch schemaFile {
	case "sdlc-config.schema.json":
		return "config"
	case "sdlc-local.schema.json":
		return "local"
	default:
		return ""
	}
}

// schemaLeafKeys returns the sorted dotted path of every leaf in doc's
// "properties" tree. A leaf is a property whose node (after "$ref") has no
// "properties" of its own: scalars, arrays, and free-form tables such as
// ship.steps or workspace.branch.typeMap.
func schemaLeafKeys(doc rawSchema) []string {
	var keys []string
	var walk func(node map[string]interface{}, prefix string)
	walk = func(node map[string]interface{}, prefix string) {
		props := asObject(node["properties"])
		for name, raw := range props {
			child := resolveRef(doc, asObject(raw))
			path := name
			if prefix != "" {
				path = prefix + "." + name
			}
			if child != nil && asObject(child["properties"]) != nil {
				walk(child, path)
				continue
			}
			keys = append(keys, path)
		}
	}
	walk(map[string]interface{}{"properties": doc["properties"]}, "")
	slices.Sort(keys)
	return keys
}

// labelledLeafKeys returns the leaf keys of doc as "<kind>:<dotted path>".
func labelledLeafKeys(doc rawSchema, kind string) []string {
	keys := schemaLeafKeys(doc)
	for i, k := range keys {
		keys[i] = kind + ":" + k
	}
	return keys
}

// keyUnder reports whether key equals prefix or sits below it in the dotted
// path tree.
func keyUnder(key, prefix string) bool {
	return key == prefix || strings.HasPrefix(key, prefix+".")
}

// coveredBySection applies coverage orders 1 and 2 to one labelled leaf key
// ("<config|local>:<dotted path>"). Order 1: the path equals ConfigPath +
// "." + Field.Name of a form field. Order 2: the path equals or is below the
// ConfigPath of a delegated section; a delegated section whose ConfigPath is
// "" or "<managed-block>" is skipped.
func coveredBySection(label string, sections []Section) bool {
	kind, path, _ := strings.Cut(label, ":")
	for _, s := range sections {
		if schemaKeyKind(schemaFileForConfigFile(s.ConfigFile)) != kind {
			continue
		}
		for _, f := range s.Fields {
			if path == s.ConfigPath+"."+f.Name {
				return true
			}
		}
		if s.DelegatedTo != "" && s.ConfigPath != "" && s.ConfigPath != "<managed-block>" && keyUnder(path, s.ConfigPath) {
			return true
		}
	}
	return false
}

// schemaKeyCoverageFailures checks the labelled leaf keys against the
// sections and the entry map, and returns one message per problem:
//   - a leaf with no form field, no delegated section and no entry
//   - an entry for a key that a form field or a delegated section covers
//   - an entry for a key that is not a schema leaf (one message only)
func schemaKeyCoverageFailures(leaves []string, sections []Section, entries map[string]string) []string {
	var failures []string
	isLeaf := map[string]bool{}
	for _, label := range leaves {
		isLeaf[label] = true
		_, hasEntry := entries[label]
		if !coveredBySection(label, sections) && !hasEntry {
			failures = append(failures, label+": schema key is not covered; add a form field, add a delegated section, or add an entry to uncoveredSchemaKeys")
		}
	}
	for label := range entries {
		switch {
		case !isLeaf[label]:
			failures = append(failures, label+": uncoveredSchemaKeys entry names a key that is not a schema leaf; remove it")
		case coveredBySection(label, sections):
			failures = append(failures, label+": uncoveredSchemaKeys entry names a key that a form field or delegated section covers; remove it")
		}
	}
	slices.Sort(failures)
	return failures
}

// TestSchemaKeys_CoveredByFormOrDelegated runs the coverage check on the real
// schemas, the real sections and the real uncoveredSchemaKeys map.
func TestSchemaKeys_CoveredByFormOrDelegated(t *testing.T) {
	var leaves []string
	for _, schemaFile := range []string{"sdlc-config.schema.json", "sdlc-local.schema.json"} {
		leaves = append(leaves, labelledLeafKeys(loadRawSchema(t, schemaFile), schemaKeyKind(schemaFile))...)
	}
	for _, failure := range schemaKeyCoverageFailures(leaves, Sections(), uncoveredSchemaKeys) {
		t.Error(failure)
	}
}

// TestSchemaKeys_GuardCases proves the coverage check on a small schema and
// sections in memory, with an injected entry map.
func TestSchemaKeys_GuardCases(t *testing.T) {
	const miniSchema = `{"properties":{"ship":{"properties":{"bump":{"type":"string"},"newKey":{"type":"string"}}}}}`
	shipSection := []Section{{
		ID:         "ship",
		ConfigFile: ".sdlc-v2/local.toml",
		ConfigPath: "ship",
		Fields:     []Field{{Name: "bump"}},
	}}
	leavesOf := func(t *testing.T, schema string) []string {
		t.Helper()
		var doc rawSchema
		if err := json.Unmarshal([]byte(schema), &doc); err != nil {
			t.Fatalf("parse mini schema: %v", err)
		}
		return labelledLeafKeys(doc, "local")
	}

	t.Run("new key", func(t *testing.T) {
		got := schemaKeyCoverageFailures(leavesOf(t, miniSchema), shipSection, map[string]string{})
		if len(got) != 1 || !strings.Contains(got[0], "local:ship.newKey") {
			t.Fatalf("want one failure naming local:ship.newKey, got %v", got)
		}
		for _, way := range []string{"form field", "delegated section", "uncoveredSchemaKeys"} {
			if !strings.Contains(got[0], way) {
				t.Errorf("failure %q does not name the way %q", got[0], way)
			}
		}
	})

	t.Run("stale entry", func(t *testing.T) {
		entries := map[string]string{
			"local:ship.newKey": "Set it by hand in .sdlc-v2/local.toml.",
			"local:ship.bump":   "Set it by hand in .sdlc-v2/local.toml.",
		}
		got := schemaKeyCoverageFailures(leavesOf(t, miniSchema), shipSection, entries)
		if len(got) != 1 || !strings.Contains(got[0], "local:ship.bump") {
			t.Fatalf("want one failure naming local:ship.bump, got %v", got)
		}
	})

	t.Run("unknown entry", func(t *testing.T) {
		entries := map[string]string{
			"local:ship.newKey":    "Set it by hand in .sdlc-v2/local.toml.",
			"local:ship.noSuchKey": "Set it by hand in .sdlc-v2/local.toml.",
		}
		got := schemaKeyCoverageFailures(leavesOf(t, miniSchema), shipSection, entries)
		if len(got) != 1 || !strings.Contains(got[0], "local:ship.noSuchKey") {
			t.Fatalf("want one failure naming local:ship.noSuchKey, got %v", got)
		}
	})

	t.Run("covered", func(t *testing.T) {
		const bumpOnly = `{"properties":{"ship":{"properties":{"bump":{"type":"string"}}}}}`
		if got := schemaKeyCoverageFailures(leavesOf(t, bumpOnly), shipSection, map[string]string{}); len(got) != 0 {
			t.Fatalf("want no failure, got %v", got)
		}
	})
}
