package tools

import (
	"context"
	"encoding/json"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
)

// configSuffixRE matches the required trailing sentence on the
// jsonschema_description of any field tagged `sdlcconfig:"..."` (KD-9): the
// field is optional and falls back to a named config key when omitted.
var configSuffixRE = regexp.MustCompile(`Optional\. Defaults to config \S+\. Pass only to override\.$`)

// inputTypes mirrors the TIn type argument of every mcpserver.Register call
// reachable from the Register*Tools functions invoked below, keyed by MCP
// tool name (not by JSON field name, to avoid cross-tool field-name
// collisions -- e.g. both execute_state and ship carry a "quality" field).
// There is currently no runtime registry of registered input types
// (mcpserver.Register does not retain TIn's reflect.Type on *Server), so this
// list is maintained by hand; a missing entry fails loudly below rather than
// silently skipping a tool's config-suffix check.
var inputTypes = map[string]reflect.Type{
	"dimensions_render_instructions": reflect.TypeOf(DimensionsRenderInstructionsIn{}),
	"learnings_log":                  reflect.TypeOf(LearningsLogIn{}),
	"commit_prepare":                 reflect.TypeOf(CommitPrepareIn{}),
	"commit_apply":                   reflect.TypeOf(CommitApplyIn{}),
	"plan_support":                   reflect.TypeOf(PlanSupportIn{}),
	"migrate":                        reflect.TypeOf(MigrateIn{}),
	"poll_await":                     reflect.TypeOf(PollAwaitIn{}),
	"verify_pipeline_classify":       reflect.TypeOf(VerifyPipelineClassifyIn{}),
	"plan_explore_prepare":           reflect.TypeOf(PlanExploreIn{}),
	"execute_state":                  reflect.TypeOf(ExecuteStateIn{}),
	"prepare_orchestrator":           reflect.TypeOf(PrepareOrchestratorIn{}),
	"jira":                           reflect.TypeOf(JiraIn{}),
	"received_review_prepare":        reflect.TypeOf(ReceivedReviewIn{}),
	"received_review_verify":         reflect.TypeOf(ReceivedReviewVerifyIn{}),
	"links_validate":                 reflect.TypeOf(LinksValidateIn{}),
	"mcp_failure_record":             reflect.TypeOf(MCPFailureRecordIn{}),
	"openspec_enrich":                reflect.TypeOf(OpenspecEnrichIn{}),
	"scaffold_ci":                    reflect.TypeOf(ScaffoldCIIn{}),
	"verify_tag_ancestry":            reflect.TypeOf(VerifyTagAncestryIn{}),
	"plan_prepare":                   reflect.TypeOf(PlanPrepareIn{}),
	"plan_mark":                      reflect.TypeOf(PlanMarkIn{}),
	"setup_write_sections":           reflect.TypeOf(SetupWriteSectionsIn{}),
	"pr_prepare":                     reflect.TypeOf(PRPrepareIn{}),
	"pr_apply":                       reflect.TypeOf(PRApplyIn{}),
	"review_prepare":                 reflect.TypeOf(ReviewPrepareIn{}),
	"setup_prepare":                  reflect.TypeOf(SetupPrepareIn{}),
	"setup_init":                     reflect.TypeOf(SetupInitIn{}),
	"ship_prepare":                   reflect.TypeOf(ShipPrepareIn{}),
	"ship_verify_side_effect":        reflect.TypeOf(ShipVerifySideEffectIn{}),
	"validate":                       reflect.TypeOf(ValidateIn{}),
	"ship_state":                     reflect.TypeOf(ShipStateIn{}),
}

// taggedFieldsFor builds a JSON-property-name -> sdlcconfig-key map for a
// tool's *In struct by walking its top-level fields (KD-2: fields are flat,
// no nesting, so a single non-recursive pass over typ's fields is enough).
// Fields without an sdlcconfig tag are absent from the result and so are not
// subject to the suffix rule.
func taggedFieldsFor(typ reflect.Type) map[string]string {
	tagged := map[string]string{}
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		key, ok := f.Tag.Lookup("sdlcconfig")
		if !ok {
			continue
		}
		jsonTag := f.Tag.Get("json")
		name := strings.Split(jsonTag, ",")[0]
		if name == "" || name == "-" {
			continue
		}
		tagged[name] = key
	}
	return tagged
}

// TestMCPToolParameterDescriptions verifies every parameter exposed by every
// registered MCP tool's input schema carries a non-empty jsonschema_description
// (recursing into nested object properties and array-of-object item
// properties). A caller with no other context — including an LLM driving the
// tool — relies on these descriptions to know what each field means; a field
// without one is effectively undocumented at the call site.
func TestMCPToolParameterDescriptions(t *testing.T) {
	s := mcpserver.New("test", "0.0.0-test")

	// Mirrors cmd/sdlc/main.go's runMCP registration list exactly, so this
	// test covers the same tool surface the real server exposes.
	RegisterPlanTools(s)
	RegisterPlanExploreTools(s)
	RegisterLinksTools(s)
	RegisterMCPFailureTools(s)
	RegisterReviewTools(s)
	RegisterExecuteStateTools(s)
	RegisterReceivedReviewTools(s)
	RegisterCommitTools(s)
	RegisterScaffoldTools(s)
	RegisterSetupTools(s)
	RegisterSetupWriteTools(s)
	RegisterPRTools(s)
	RegisterPrepareOrchestratorTools(s)
	RegisterOpenspecTools(s)
	RegisterDimensionsRenderTools(s)
	RegisterValidateTools(s)
	RegisterShipStateTools(s)
	RegisterJiraTools(s)
	RegisterPollingTools(s)
	RegisterMigrateTools(s)
	RegisterShipTools(s)
	RegisterPlanSupportTools(s)
	RegisterLearningsTools(s)

	ctx := context.Background()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	if _, err := s.MCPServer().Connect(ctx, serverTransport, nil); err != nil {
		t.Fatalf("server Connect: %v", err)
	}

	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0.0.0"}, nil)
	c, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client Connect: %v", err)
	}
	t.Cleanup(func() { c.Close() })

	resp, err := c.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(resp.Tools) == 0 {
		t.Fatal("ListTools: no tools registered")
	}

	for _, tool := range resp.Tools {
		raw, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Fatalf("%s: marshal input schema: %v", tool.Name, err)
		}
		var schema map[string]any
		if err := json.Unmarshal(raw, &schema); err != nil {
			t.Fatalf("%s: unmarshal input schema: %v", tool.Name, err)
		}

		typ, ok := inputTypes[tool.Name]
		if !ok {
			t.Errorf("%s: no entry in inputTypes -- add this tool's *In type so its sdlcconfig tags are checked", tool.Name)
			checkSchemaPropertyDescriptions(t, tool.Name, "", schema, nil)
			continue
		}
		checkSchemaPropertyDescriptions(t, tool.Name, "", schema, taggedFieldsFor(typ))
	}
}

// checkSchemaPropertyDescriptions recursively walks a JSON-schema object
// node, failing on any property without a non-empty "description". It
// descends into nested object properties and into array items that are
// themselves objects. taggedFields maps a top-level JSON property name to
// its sdlcconfig key (KD-2: flat fields only -- nested/array properties are
// never present in taggedFields, so the suffix rule only ever fires at the
// top level); for each such property, its description must additionally end
// with configSuffixRE's required suffix.
func checkSchemaPropertyDescriptions(t *testing.T, toolName, path string, schema map[string]any, taggedFields map[string]string) {
	t.Helper()

	props, _ := schema["properties"].(map[string]any)
	for name, rawProp := range props {
		prop, ok := rawProp.(map[string]any)
		if !ok {
			continue
		}
		fieldPath := path + "." + name

		desc, _ := prop["description"].(string)
		if desc == "" {
			t.Errorf("%s: field %q has no jsonschema_description", toolName, fieldPath)
		}

		if configKey, ok := taggedFields[name]; ok && desc != "" && !configSuffixRE.MatchString(desc) {
			t.Errorf("%s: field %q is sdlcconfig-tagged (config key %q) but its description %q does not end with the required \"Optional. Defaults to config <key>. Pass only to override.\" suffix", toolName, fieldPath, configKey, desc)
		}

		if nestedProps, ok := prop["properties"].(map[string]any); ok {
			checkSchemaPropertyDescriptions(t, toolName, fieldPath, map[string]any{"properties": nestedProps}, nil)
		}
		if items, ok := prop["items"].(map[string]any); ok {
			if itemProps, ok := items["properties"].(map[string]any); ok {
				checkSchemaPropertyDescriptions(t, toolName, fieldPath+"[]", map[string]any{"properties": itemProps}, nil)
			}
		}
	}
}

// TestConfigSuffixRE pins down configSuffixRE's accept/reject behavior
// directly, independent of any live tool schema. This is the durable proof
// that the regex actually rejects a missing or malformed suffix (AC1);
// TestMCPToolParameterDescriptions passing with the real sdlcconfig tags
// only proves today's descriptions happen to satisfy it.
func TestConfigSuffixRE(t *testing.T) {
	cases := []struct {
		desc string
		want bool
	}{
		{"Quality level to stamp on init. Optional. Defaults to config execute.quality. Pass only to override.", true},
		{"Optional. Defaults to config state.gc.ttlDays. Pass only to override.", true},
		{"Quality level to stamp on init.", false},                                     // suffix missing entirely
		{"Optional. Defaults to config execute.quality. Pass only to override", false}, // missing trailing period
		{"Defaults to config execute.quality. Pass only to override.", false},          // missing leading "Optional."
		{"Optional. Defaults to config execute.quality. Pass only to override. Extra trailing text.", false}, // suffix not at end
	}
	for _, c := range cases {
		if got := configSuffixRE.MatchString(c.desc); got != c.want {
			t.Errorf("configSuffixRE.MatchString(%q) = %v, want %v", c.desc, got, c.want)
		}
	}
}
