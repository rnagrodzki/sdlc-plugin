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
// alternatives, or (for an array-typed node) read from its "items" schema.
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
	return nil
}

// TestFieldOptions_AcceptedBySchema finds the schema of each section through
// Section.ConfigFile and Section.ConfigPath, then checks that every value in
// each "enum" or "multi-enum" Field's Options is accepted by the matching
// schema property (found through Field.Name, which may be dotted, e.g.
// "versionFile.fileType"). A field whose schema node declares no enum at all
// (e.g. one validated by "pattern") is out of scope and skipped. A genuine
// mismatch fails the test unless listed in optionExceptions with a reason.
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
			if f.Type != "enum" && f.Type != "multi-enum" {
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
			if len(enumValues) == 0 {
				// The schema validates this field by some other means
				// (e.g. "pattern"); nothing to compare against Options.
				continue
			}

			var missing []string
			for _, opt := range f.Options {
				if !slices.Contains(enumValues, opt) {
					missing = append(missing, opt)
				}
			}
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

// TestReviewMaxDimensionsSchemaMatchesField pins that the review.maxDimensions
// setup field and its JSON schema property stay in sync on type, minimum,
// and default. TestFieldOptions_AcceptedBySchema above only compares
// enum/multi-enum fields against the schema, so a "number" field like this
// one needs its own guard against drift.
func TestReviewMaxDimensionsSchemaMatchesField(t *testing.T) {
	var field *Field
	for _, section := range Sections() {
		if section.ID != "review" {
			continue
		}
		for i := range section.Fields {
			if section.Fields[i].Name == "maxDimensions" {
				field = &section.Fields[i]
			}
		}
	}
	if field == nil {
		t.Fatal("review section has no maxDimensions field")
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
	node := schemaNodeFromNode(doc, root, "review.maxDimensions")
	if node == nil {
		t.Fatal("schema has no property at review.maxDimensions")
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
