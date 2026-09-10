package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
)

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
	RegisterVersionTools(s)
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

	c, err := client.NewInProcessClient(s.MCPServer())
	if err != nil {
		t.Fatalf("NewInProcessClient: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	if err := c.Start(context.Background()); err != nil {
		t.Fatalf("client.Start: %v", err)
	}

	initReq := mcp.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{Name: "test", Version: "0.0.0"}
	if _, err := c.Initialize(context.Background(), initReq); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	resp, err := c.ListTools(context.Background(), mcp.ListToolsRequest{})
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
		checkSchemaPropertyDescriptions(t, tool.Name, "", schema)
	}
}

// checkSchemaPropertyDescriptions recursively walks a JSON-schema object
// node, failing on any property without a non-empty "description". It
// descends into nested object properties and into array items that are
// themselves objects.
func checkSchemaPropertyDescriptions(t *testing.T, toolName, path string, schema map[string]any) {
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

		if nestedProps, ok := prop["properties"].(map[string]any); ok {
			checkSchemaPropertyDescriptions(t, toolName, fieldPath, map[string]any{"properties": nestedProps})
		}
		if items, ok := prop["items"].(map[string]any); ok {
			if itemProps, ok := items["properties"].(map[string]any); ok {
				checkSchemaPropertyDescriptions(t, toolName, fieldPath+"[]", map[string]any{"properties": itemProps})
			}
		}
	}
}
