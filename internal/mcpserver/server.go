package mcpserver

import (
	"context"
	"os"
	"os/signal"
	"slices"
	"sync"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Server wraps the official MCP SDK's Server with typed registration helpers.
type Server struct {
	mcp *mcp.Server

	// The go-sdk server has no accessor that lists its tools, so Register
	// records each tool name and the enum of its "action" input field here.
	// mu guards tools. A second registration of one name replaces the entry,
	// as the go-sdk server does.
	mu    sync.RWMutex
	tools map[string][]string
}

// New creates a Server backed by a new mcp.Server instance.
func New(name, version string) *Server {
	return &Server{
		mcp:   mcp.NewServer(&mcp.Implementation{Name: name, Version: version}, nil),
		tools: map[string][]string{},
	}
}

// recordTool stores the tool name and the values of its "action" enum.
// A tool without an "action" enum has an empty (non-nil) actions slice.
func (s *Server) recordTool(name string, actions []string) {
	if actions == nil {
		actions = []string{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tools == nil {
		s.tools = map[string][]string{}
	}
	s.tools[name] = actions
}

// ToolNames returns the names of the registered tools, sorted. The result is
// never nil.
func (s *Server) ToolNames() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	names := make([]string, 0, len(s.tools))
	for name := range s.tools {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// ToolActions returns the values of the "action" enum in the input schema of
// the named tool, in schema order. A registered tool with no "action" enum
// gives an empty slice and true. An unknown name gives an empty slice and
// false. The result is never nil, and it is a copy the caller may change.
func (s *Server) ToolActions(name string) (actions []string, ok bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	stored, ok := s.tools[name]
	return append([]string{}, stored...), ok
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
