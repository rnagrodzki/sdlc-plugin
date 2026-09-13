package hooks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
	"github.com/rnagrodzki/sdlc-plugin/internal/tools"
)

// mcpEvidenceFile returns the path recordMCPInvocation is expected to write
// to, mirroring tools.mcpEvidencePath (unexported, so duplicated here as a
// literal path build rather than imported).
func mcpEvidenceFile(root string) string {
	return filepath.Join(root, paths.DataDir, "evidence", "mcp-invocations.jsonl")
}

func readMCPEvidenceLines(t *testing.T, root string) []tools.MCPEvidenceEntry {
	t.Helper()
	b, err := os.ReadFile(mcpEvidenceFile(root))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("read mcp evidence: %v", err)
	}
	var out []tools.MCPEvidenceEntry
	for _, line := range splitNonEmptyLines(string(b)) {
		var e tools.MCPEvidenceEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("unmarshal mcp evidence line %q: %v", line, err)
		}
		out = append(out, e)
	}
	return out
}

func splitNonEmptyLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			if line := s[start:i]; line != "" {
				lines = append(lines, line)
			}
			start = i + 1
		}
	}
	if line := s[start:]; line != "" {
		lines = append(lines, line)
	}
	return lines
}

func TestRecordMCPInvocation(t *testing.T) {
	t.Run("non-mcp tool name: silent, no file written", func(t *testing.T) {
		root := gitFixture(t, "feat/mcp-non-mcp-tool")
		out, err := recordMCPInvocation(HookCtx{SessionID: "s1"}, Event{ToolName: "Bash"})
		if err != nil {
			t.Fatal(err)
		}
		assertSilent(t, out)
		if _, statErr := os.Stat(mcpEvidenceFile(root)); !os.IsNotExist(statErr) {
			t.Errorf("expected no evidence file written, stat err = %v", statErr)
		}
	})

	t.Run("mcp tool name, no ship/execute state: still records with empty skillContext", func(t *testing.T) {
		root := gitFixture(t, "feat/mcp-no-state")
		out, err := recordMCPInvocation(HookCtx{SessionID: "s1"}, Event{ToolName: "mcp__plugin_sdlc_sdlc__execute_state"})
		if err != nil {
			t.Fatal(err)
		}
		assertSilent(t, out)

		entries := readMCPEvidenceLines(t, root)
		if len(entries) != 1 {
			t.Fatalf("got %d entries, want 1", len(entries))
		}
		if entries[0].Tool != "mcp__plugin_sdlc_sdlc__execute_state" {
			t.Errorf("Tool = %q, want mcp__plugin_sdlc_sdlc__execute_state", entries[0].Tool)
		}
		if entries[0].SessionID != "s1" {
			t.Errorf("SessionID = %q, want s1", entries[0].SessionID)
		}
		if entries[0].SkillContext != "" {
			t.Errorf("SkillContext = %q, want empty (no ship/execute state)", entries[0].SkillContext)
		}
		if entries[0].Timestamp == "" {
			t.Error("Timestamp is empty, want a value")
		}
	})

	t.Run("mcp tool name, advancing ship state: records with skillContext=ship", func(t *testing.T) {
		root := gitFixture(t, "feat/mcp-ship-state")
		steps := []any{
			map[string]any{"name": "review", "status": "in_progress"},
		}
		newShipState(t, root, "feat/mcp-ship-state", "s1", steps, map[string]any{"auto": true})

		out, err := recordMCPInvocation(HookCtx{SessionID: "s1"}, Event{ToolName: "mcp__plugin_sdlc_sdlc__ship_state"})
		if err != nil {
			t.Fatal(err)
		}
		assertSilent(t, out)

		entries := readMCPEvidenceLines(t, root)
		if len(entries) != 1 {
			t.Fatalf("got %d entries, want 1", len(entries))
		}
		if entries[0].SkillContext != "ship" {
			t.Errorf("SkillContext = %q, want ship", entries[0].SkillContext)
		}
	})

	t.Run("two mcp calls append two lines", func(t *testing.T) {
		root := gitFixture(t, "feat/mcp-two-calls")
		for i := 0; i < 2; i++ {
			if _, err := recordMCPInvocation(HookCtx{SessionID: "s1"}, Event{ToolName: "mcp__plugin_sdlc_sdlc__validate"}); err != nil {
				t.Fatal(err)
			}
		}
		entries := readMCPEvidenceLines(t, root)
		if len(entries) != 2 {
			t.Fatalf("got %d entries, want 2", len(entries))
		}
	})
}
