package hooks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

const planPrepareTool = "mcp__plugin_sdlc_sdlc__plan_prepare"

// planRunSessionID reads the sessionId of the plan state file at path. It
// returns "" when the key is absent, null, or not a string.
func planRunSessionID(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read plan state: %v", err)
	}
	var data map[string]any
	if err := json.Unmarshal(b, &data); err != nil {
		t.Fatalf("unmarshal plan state: %v", err)
	}
	id, _ := data["sessionId"].(string)
	return id
}

// activePlanMarkers returns a planIntegrity map for a run that is in flight:
// skillInvoked set, done absent.
func activePlanMarkers() map[string]any {
	return map[string]any{"skillInvoked": "2024-01-01T00:00:00Z"}
}

// callPlanPrepareHook runs the hook for a plan_prepare event in sessionID and
// asserts the result is silent.
func callPlanPrepareHook(t *testing.T, sessionID string) {
	t.Helper()
	out, err := recordMCPInvocation(HookCtx{SessionID: sessionID}, Event{ToolName: planPrepareTool})
	if err != nil {
		t.Fatal(err)
	}
	assertSilent(t, out)
}

func TestRecordMCPInvocation_PlanSessionStamp(t *testing.T) {
	t.Run("first plan_prepare stamps the session ID", func(t *testing.T) {
		root := gitFixture(t, "feat/plan-stamp-first")
		st := newPlanState(t, root, "feat/plan-stamp-first", activePlanMarkers(), "")
		if got := planRunSessionID(t, st.Path); got != "" {
			t.Fatalf("precondition: sessionId = %q, want empty", got)
		}

		callPlanPrepareHook(t, "s-1")

		if got := planRunSessionID(t, st.Path); got != "s-1" {
			t.Errorf("sessionId = %q, want s-1", got)
		}
	})

	t.Run("resume in a new session replaces the old ID", func(t *testing.T) {
		root := gitFixture(t, "feat/plan-stamp-resume")
		st := newPlanState(t, root, "feat/plan-stamp-resume", activePlanMarkers(), "")

		callPlanPrepareHook(t, "s-1")
		callPlanPrepareHook(t, "s-2")

		if got := planRunSessionID(t, st.Path); got != "s-2" {
			t.Errorf("sessionId = %q, want s-2", got)
		}
	})

	t.Run("same session ID: no state write", func(t *testing.T) {
		root := gitFixture(t, "feat/plan-stamp-same")
		st := newPlanState(t, root, "feat/plan-stamp-same", activePlanMarkers(), "")
		callPlanPrepareHook(t, "s-2")

		old := time.Now().Add(-time.Hour).Truncate(time.Second)
		if err := os.Chtimes(st.Path, old, old); err != nil {
			t.Fatal(err)
		}
		callPlanPrepareHook(t, "s-2")

		info, err := os.Stat(st.Path)
		if err != nil {
			t.Fatal(err)
		}
		if !info.ModTime().Equal(old) {
			t.Errorf("state file mtime = %v, want %v (no write)", info.ModTime(), old)
		}
		if got := planRunSessionID(t, st.Path); got != "s-2" {
			t.Errorf("sessionId = %q, want s-2", got)
		}
	})

	t.Run("another tool name: no stamp", func(t *testing.T) {
		root := gitFixture(t, "feat/plan-stamp-other-tool")
		st := newPlanState(t, root, "feat/plan-stamp-other-tool", activePlanMarkers(), "")

		out, err := recordMCPInvocation(HookCtx{SessionID: "s-1"}, Event{ToolName: "mcp__plugin_sdlc_sdlc__plan_mark"})
		if err != nil {
			t.Fatal(err)
		}
		assertSilent(t, out)

		if got := planRunSessionID(t, st.Path); got != "" {
			t.Errorf("sessionId = %q, want empty", got)
		}
	})

	t.Run("empty session ID: no stamp", func(t *testing.T) {
		root := gitFixture(t, "feat/plan-stamp-empty-id")
		st := newPlanState(t, root, "feat/plan-stamp-empty-id", activePlanMarkers(), "")

		callPlanPrepareHook(t, "")

		if got := planRunSessionID(t, st.Path); got != "" {
			t.Errorf("sessionId = %q, want empty", got)
		}
	})

	t.Run("no plan run: silent, no state file created", func(t *testing.T) {
		root := gitFixture(t, "feat/plan-stamp-no-run")

		callPlanPrepareHook(t, "s-1")

		matches, err := filepath.Glob(filepath.Join(root, paths.DataDir, paths.RunsSubdir, "plan-*.json"))
		if err != nil {
			t.Fatal(err)
		}
		if len(matches) != 0 {
			t.Errorf("plan state files = %v, want none", matches)
		}
	})

	t.Run("done plan run: no stamp", func(t *testing.T) {
		root := gitFixture(t, "feat/plan-stamp-done")
		st := newPlanState(t, root, "feat/plan-stamp-done", allPlanMarkers(), "")

		callPlanPrepareHook(t, "s-1")

		if got := planRunSessionID(t, st.Path); got != "" {
			t.Errorf("sessionId = %q, want empty", got)
		}
	})

	t.Run("state read error: silent, one stderr line", func(t *testing.T) {
		root := gitFixture(t, "feat/plan-stamp-read-error")
		st := newPlanState(t, root, "feat/plan-stamp-read-error", activePlanMarkers(), "")
		if err := os.WriteFile(st.Path, []byte("{not json"), 0o644); err != nil {
			t.Fatal(err)
		}

		var out Output
		stderr := captureStderr(t, func() {
			var err error
			out, err = recordMCPInvocation(HookCtx{SessionID: "s-1"}, Event{ToolName: planPrepareTool})
			if err != nil {
				t.Fatal(err)
			}
		})
		assertSilent(t, out)
		assertPlanStampStderr(t, stderr)
	})

	t.Run("state write error: silent, one stderr line", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("a folder mode does not stop the root user")
		}
		root := gitFixture(t, "feat/plan-stamp-write-error")
		st := newPlanState(t, root, "feat/plan-stamp-write-error", activePlanMarkers(), "")

		runsDir := filepath.Dir(st.Path)
		if err := os.Chmod(runsDir, 0o500); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(runsDir, 0o700) })

		var out Output
		stderr := captureStderr(t, func() {
			var err error
			out, err = recordMCPInvocation(HookCtx{SessionID: "s-1"}, Event{ToolName: planPrepareTool})
			if err != nil {
				t.Fatal(err)
			}
		})
		assertSilent(t, out)
		assertPlanStampStderr(t, stderr)

		if err := os.Chmod(runsDir, 0o700); err != nil {
			t.Fatal(err)
		}
		if got := planRunSessionID(t, st.Path); got != "" {
			t.Errorf("sessionId = %q, want empty (write failed)", got)
		}
	})
}

// assertPlanStampStderr checks that stderr holds exactly one line that starts
// with the plan session stamp prefix.
func assertPlanStampStderr(t *testing.T, stderr string) {
	t.Helper()
	lines := splitNonEmptyLines(stderr)
	if len(lines) != 1 {
		t.Fatalf("stderr has %d lines, want 1: %q", len(lines), stderr)
	}
	if !strings.HasPrefix(lines[0], "sdlc hook: plan session stamp: ") {
		t.Errorf("stderr line = %q, want prefix %q", lines[0], "sdlc hook: plan session stamp: ")
	}
}
