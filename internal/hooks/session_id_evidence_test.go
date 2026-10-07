package hooks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
	"github.com/rnagrodzki/sdlc-plugin/internal/tools"
)

// cliEvidenceFilePath mirrors tools.cliEvidencePath (unexported), matching
// mcpEvidenceFile/userInputEvidenceFile's existing duplicated-literal-path
// pattern (mcp_invocation_record_test.go, user_input_record_test.go).
func cliEvidenceFilePath(root string) string {
	return filepath.Join(root, paths.DataDir, paths.EvidenceSubdir, "cli-executions.jsonl")
}

// readRawJSONLLastLine returns the last non-empty line of path as a raw
// string, for asserting on the exact JSON keys written (e.g. that
// "sessionId" is present even when empty) rather than on a decoded struct,
// which would hide a missing key behind its zero value.
func readRawJSONLLastLine(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	lines := splitNonEmptyLines(string(b))
	if len(lines) == 0 {
		t.Fatalf("%s has no lines", path)
	}
	return lines[len(lines)-1]
}

// TestSessionIDOnCLIEvidence covers Task 3's acceptance criteria for
// CLIEvidenceEntry.SessionID: a recorded command entry carries the hook's
// session id, an empty session id still writes the "sessionId" key (not
// omitempty), and a pre-existing line with no sessionId key at all still
// parses without error.
func TestSessionIDOnCLIEvidence(t *testing.T) {
	t.Run("command entry from an active ship run carries the hook session id", func(t *testing.T) {
		root := gitFixture(t, "feat/sid-cli-active")
		branch := "feat/sid-cli-active"
		newShipState(t, root, branch, "s1", []any{
			map[string]any{"name": "review", "status": "in_progress"},
		}, map[string]any{"auto": true})

		if _, err := pipelineContinue(HookCtx{SessionID: "hook-sess-1"}, bashEvent("go test ./...", map[string]any{"stdout": "ok"})); err != nil {
			t.Fatal(err)
		}

		entry, ok, err := tools.LastCLIEvidenceEntry(root)
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			t.Fatal("expected a CLI evidence entry to have been recorded")
		}
		if entry.SessionID != "hook-sess-1" {
			t.Errorf("SessionID = %q, want hook-sess-1", entry.SessionID)
		}
	})

	t.Run("empty session id still writes the sessionId key, as an empty string", func(t *testing.T) {
		root := gitFixture(t, "feat/sid-cli-empty")
		branch := "feat/sid-cli-empty"
		newShipState(t, root, branch, "s1", []any{
			map[string]any{"name": "review", "status": "in_progress"},
		}, map[string]any{"auto": true})

		if _, err := pipelineContinue(HookCtx{SessionID: ""}, bashEvent("go build ./...", map[string]any{"stdout": "built"})); err != nil {
			t.Fatal(err)
		}

		raw := readRawJSONLLastLine(t, cliEvidenceFilePath(root))
		var m map[string]any
		if err := json.Unmarshal([]byte(raw), &m); err != nil {
			t.Fatalf("unmarshal %q: %v", raw, err)
		}
		v, present := m["sessionId"]
		if !present {
			t.Fatal("sessionId key missing from the written line, want it always present")
		}
		if v != "" {
			t.Errorf("sessionId = %v, want empty string", v)
		}
	})

	t.Run("an old line with no sessionId key at all still parses", func(t *testing.T) {
		root := gitFixture(t, "feat/sid-cli-old-line")
		path := cliEvidenceFilePath(root)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		oldLine := `{"ts":"2026-01-01T00:00:00Z","pipeline":"ship","step":"review","branch":"feat/x","command":"go test ./...","exitCode":0,"outputHead":"ok"}` + "\n"
		if err := os.WriteFile(path, []byte(oldLine), 0644); err != nil {
			t.Fatal(err)
		}

		entry, ok, err := tools.LastCLIEvidenceEntry(root)
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			t.Fatal("expected the pre-existing line to be read back")
		}
		if entry.SessionID != "" {
			t.Errorf("SessionID = %q, want empty (line predates the field)", entry.SessionID)
		}
		if entry.Command != "go test ./..." {
			t.Errorf("Command = %q, want %q", entry.Command, "go test ./...")
		}
	})
}

// TestSessionIDOnUserInputEvidence mirrors TestSessionIDOnCLIEvidence for
// UserInputEntry.SessionID: a recorded prompt entry carries the hook's
// session id, an empty session id still writes the "sessionId" key, and a
// pre-existing line with no sessionId key still parses.
func TestSessionIDOnUserInputEvidence(t *testing.T) {
	t.Run("prompt entry from an active ship run carries the hook session id", func(t *testing.T) {
		root := gitFixture(t, "feat/sid-ui-active")
		branch := "feat/sid-ui-active"
		newShipState(t, root, branch, "s1", []any{
			map[string]any{"name": "review", "status": "in_progress"},
		}, nil)

		out, err := recordUserInput(HookCtx{SessionID: "hook-sess-2"}, Event{Raw: map[string]any{
			"prompt_text": "skip the low findings",
		}})
		if err != nil {
			t.Fatal(err)
		}
		assertSilent(t, out)

		entries := readUserInputLines(t, root)
		if len(entries) != 1 {
			t.Fatalf("got %d entries, want 1", len(entries))
		}
		if entries[0].SessionID != "hook-sess-2" {
			t.Errorf("SessionID = %q, want hook-sess-2", entries[0].SessionID)
		}
	})

	t.Run("empty session id still writes the sessionId key, as an empty string", func(t *testing.T) {
		root := gitFixture(t, "feat/sid-ui-empty")
		branch := "feat/sid-ui-empty"
		newShipState(t, root, branch, "s1", []any{
			map[string]any{"name": "review", "status": "in_progress"},
		}, nil)

		out, err := recordUserInput(HookCtx{SessionID: ""}, Event{Raw: map[string]any{
			"prompt_text": "anything",
		}})
		if err != nil {
			t.Fatal(err)
		}
		assertSilent(t, out)

		raw := readRawJSONLLastLine(t, userInputEvidenceFile(root))
		var m map[string]any
		if err := json.Unmarshal([]byte(raw), &m); err != nil {
			t.Fatalf("unmarshal %q: %v", raw, err)
		}
		v, present := m["sessionId"]
		if !present {
			t.Fatal("sessionId key missing from the written line, want it always present")
		}
		if v != "" {
			t.Errorf("sessionId = %v, want empty string", v)
		}
	})

	t.Run("an old line with no sessionId key at all still parses", func(t *testing.T) {
		root := gitFixture(t, "feat/sid-ui-old-line")
		path := userInputEvidenceFile(root)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		oldLine := `{"ts":"2026-01-01T00:00:00Z","pipeline":"ship","step":"review","branch":"feat/x","text":"old prompt","kind":"prompt"}` + "\n"
		if err := os.WriteFile(path, []byte(oldLine), 0644); err != nil {
			t.Fatal(err)
		}

		entries := readUserInputLines(t, root)
		if len(entries) != 1 {
			t.Fatalf("got %d entries, want 1", len(entries))
		}
		if entries[0].SessionID != "" {
			t.Errorf("SessionID = %q, want empty (line predates the field)", entries[0].SessionID)
		}
		if entries[0].Text != "old prompt" {
			t.Errorf("Text = %q, want %q", entries[0].Text, "old prompt")
		}
	})
}
