package tools

import (
	"path/filepath"

	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
)

// MCPEvidenceEntry is one sdlc MCP tool invocation record in the evidence
// log (Task 10). Unlike CLIEvidenceEntry, it carries no durationMs: a
// PostToolUse-only hook cannot measure invocation duration without a paired
// PreToolUse timestamp, and pairing the two is out of this task's scope.
type MCPEvidenceEntry struct {
	Timestamp    string `json:"ts"`
	Tool         string `json:"tool"`
	SessionID    string `json:"sessionId"`
	SkillContext string `json:"skillContext"`
}

// mcpEvidencePath returns the path to the MCP invocation evidence JSONL file.
func mcpEvidencePath(root string) string {
	return filepath.Join(root, paths.DataDir, "evidence", "mcp-invocations.jsonl")
}

// appendMCPEvidence appends one JSONL line to
// .sdlc-v2/evidence/mcp-invocations.jsonl. Creates directory and file if
// absent. Append-only, bounded by appendJSONLBounded (cli_evidence.go).
func appendMCPEvidence(root string, entry MCPEvidenceEntry) error {
	return appendJSONLBounded(mcpEvidencePath(root), entry)
}

// AppendMCPEvidence delegates to appendMCPEvidence for internal/hooks
// (Task 10 exported-wrapper precedent — see cli_evidence.go's "Exported
// wrappers" block).
func AppendMCPEvidence(root string, entry MCPEvidenceEntry) error {
	return appendMCPEvidence(root, entry)
}
