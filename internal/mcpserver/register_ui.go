package mcpserver

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// UIResource is the MCP App view of a tool (MCP Apps 2026-01-26).
type UIResource struct {
	URI  string // must start with "ui://"
	Name string // resource name, e.g. "dashboard-card"
	HTML string // full HTML5 document; inline script and style only
}

// MCPAppMIMEType is the MIME type of an MCP App view.
const MCPAppMIMEType = "text/html;profile=mcp-app"

// MCPAppsExtension is the client capability extension key of MCP Apps
// (spec 2026-01-26, "Client<>Server Capability Negotiation"). A client that
// can show an MCP App view announces it in initialize, under
// capabilities.extensions, with the setting mimeTypes, for example
// ["text/html;profile=mcp-app"].
const MCPAppsExtension = "io.modelcontextprotocol/ui"

// SupportsMCPApps reports whether the client extensions map announces MCP
// Apps: the MCPAppsExtension key is present. It does not check mimeTypes:
// the key alone is the announcement, and a strict check could hide the view
// from a client that announces the extension with other settings.
func (c Ctx) SupportsMCPApps() bool {
	_, ok := c.ClientExtensions[MCPAppsExtension]
	return ok
}

// MaxUIHTMLBytes is the largest view HTML that RegisterUI accepts (1 MiB).
const MaxUIHTMLBytes = 1048576

// RegisterUI is Register plus an MCP App view. It sets the tool _meta
// ui.resourceUri, adds the ui:// resource, and on success also returns Out
// as structuredContent. Error results stay text only.
//
// This is the one scoped exception to the text-only output contract; see
// docs/internal/mcp-output-contract.md. RegisterUI panics at server start
// when ui.URI does not start with "ui://" or ui.HTML is larger than
// MaxUIHTMLBytes. The checks run before anything is registered, so a bad
// view leaves no tool and no resource behind.
func RegisterUI[TIn, TOut any](s *Server, name, desc string, a Annotations, ui UIResource, h func(ctx Ctx, in TIn) (TOut, error)) {
	if !strings.HasPrefix(ui.URI, "ui://") {
		panic(fmt.Sprintf("mcpserver: register %q: view URI %q does not start with ui://", name, ui.URI))
	}
	if len(ui.HTML) > MaxUIHTMLBytes {
		panic(fmt.Sprintf("mcpserver: register %q: view HTML is %d bytes, above the limit of %d", name, len(ui.HTML), MaxUIHTMLBytes))
	}

	register(s, name, desc, a, &ui, h)

	s.mcp.AddResource(&mcp.Resource{URI: ui.URI, Name: ui.Name, MIMEType: MCPAppMIMEType},
		func(context.Context, *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{
				{URI: ui.URI, MIMEType: MCPAppMIMEType, Text: ui.HTML},
			}}, nil
		})
}
