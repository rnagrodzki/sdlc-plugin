No brnach changes without user approval. Even if work is shifting stay on the approved brnahc, never change on your own.

## MCP Tool Annotations

Every MCP tool declares mcpserver.Annotations (compile-enforced). New tool → add its expected values to toolAnnotations in internal/tools/annotations_test.go. Rule and audit procedure: docs/mcp-tool-annotations.md.

## MCP Output Contract

Every MCP tool result is Markdown text — no JSON envelope, no structuredContent, no outputSchema. One shared renderer walks the `*Out` struct: a root `Next` field becomes the `**Next:**` line, `render:"raw"` fields are emitted verbatim, nil/empty collections render `(none)`, and every error carries a non-empty `## Do this` section. Rules before adding or changing a tool's output: docs/mcp-output-contract.md.