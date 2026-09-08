package mcpserver

import (
	"github.com/mark3labs/mcp-go/server"
)

// Server wraps the mcp-go MCPServer with typed registration helpers.
type Server struct {
	mcp *server.MCPServer
}

// New creates a Server backed by a new MCPServer instance.
func New(name, version string) *Server {
	return &Server{
		mcp: server.NewMCPServer(name, version,
			server.WithToolCapabilities(true),
		),
	}
}

// ServeStdio runs the MCP server over stdin/stdout with signal handling.
func (s *Server) ServeStdio() error {
	return server.ServeStdio(s.mcp)
}

// MCPServer returns the underlying *server.MCPServer, e.g. for wiring an
// in-process client in tests.
func (s *Server) MCPServer() *server.MCPServer {
	return s.mcp
}
