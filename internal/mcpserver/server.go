package mcpserver

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Server wraps the official MCP SDK's Server with typed registration helpers.
type Server struct {
	mcp *mcp.Server
}

// New creates a Server backed by a new mcp.Server instance.
func New(name, version string) *Server {
	return &Server{
		mcp: mcp.NewServer(&mcp.Implementation{Name: name, Version: version}, nil),
	}
}

// ServeStdio runs the MCP server over stdin/stdout with signal handling.
func (s *Server) ServeStdio() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return s.mcp.Run(ctx, &mcp.StdioTransport{})
}

// MCPServer returns the underlying *mcp.Server, e.g. for wiring an
// in-process client in tests.
func (s *Server) MCPServer() *mcp.Server {
	return s.mcp
}
