package hooks

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
	"github.com/rnagrodzki/sdlc-plugin/internal/state"
	"github.com/rnagrodzki/sdlc-plugin/internal/tools"
)

// userInputEvidenceFile returns the path recordUserInput is expected to
// write to, mirroring mcpEvidenceFile's (mcp_invocation_record_test.go)
// duplicated-literal-path pattern — tools.userInputPath is unexported.
func userInputEvidenceFile(root string) string {
	return filepath.Join(root, paths.DataDir, "evidence", "user-inputs.jsonl")
}

func readUserInputLines(t *testing.T, root string) []tools.UserInputEntry {
	t.Helper()
	b, err := os.ReadFile(userInputEvidenceFile(root))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("read user input evidence: %v", err)
	}
	var out []tools.UserInputEntry
	for _, line := range splitNonEmptyLines(string(b)) {
		var e tools.UserInputEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("unmarshal user input evidence line %q: %v", line, err)
		}
		out = append(out, e)
	}
	return out
}

func assertNoUserInputFile(t *testing.T, root string) {
	t.Helper()
	if _, err := os.Stat(userInputEvidenceFile(root)); !os.IsNotExist(err) {
		t.Errorf("expected no user-inputs.jsonl written, stat err = %v", err)
	}
}

func TestRecordUserInput(t *testing.T) {
	t.Run("ship step in_progress, prompt_text envelope: records pipeline/step/text", func(t *testing.T) {
		root := gitFixture(t, "feat/ui-ship-in-progress")
		branch := "feat/ui-ship-in-progress"
		newShipState(t, root, branch, "s1", []any{
			map[string]any{"name": "review", "status": "in_progress"},
		}, nil)

		out, err := recordUserInput(HookCtx{SessionID: "s1"}, Event{Raw: map[string]any{
			"hook_event_name": "UserPromptSubmit",
			"prompt_text":     "skip the low findings",
		}})
		if err != nil {
			t.Fatal(err)
		}
		assertSilent(t, out)

		entries := readUserInputLines(t, root)
		if len(entries) != 1 {
			t.Fatalf("got %d entries, want 1", len(entries))
		}
		e := entries[0]
		if e.Pipeline != "ship" {
			t.Errorf("Pipeline = %q, want ship", e.Pipeline)
		}
		if e.Step != "review" {
			t.Errorf("Step = %q, want review", e.Step)
		}
		if e.Branch != branch {
			t.Errorf("Branch = %q, want %q", e.Branch, branch)
		}
		if e.Text != "skip the low findings" {
			t.Errorf("Text = %q, want %q", e.Text, "skip the low findings")
		}
		if e.Timestamp == "" {
			t.Error("Timestamp is empty, want a value")
		}
	})

	t.Run("older envelope (prompt field instead of prompt_text): still recorded", func(t *testing.T) {
		root := gitFixture(t, "feat/ui-older-envelope")
		branch := "feat/ui-older-envelope"
		newShipState(t, root, branch, "s1", []any{
			map[string]any{"name": "review", "status": "in_progress"},
		}, nil)

		out, err := recordUserInput(HookCtx{SessionID: "s1"}, Event{Raw: map[string]any{
			"hook_event_name": "UserPromptSubmit",
			"prompt":          "older envelope text",
		}})
		if err != nil {
			t.Fatal(err)
		}
		assertSilent(t, out)

		entries := readUserInputLines(t, root)
		if len(entries) != 1 {
			t.Fatalf("got %d entries, want 1", len(entries))
		}
		if entries[0].Text != "older envelope text" {
			t.Errorf("Text = %q, want %q", entries[0].Text, "older envelope text")
		}
	})

	t.Run("ship not advancing, pr step failed: attributes to the failed step", func(t *testing.T) {
		root := gitFixture(t, "feat/ui-ship-failed")
		branch := "feat/ui-ship-failed"
		newShipState(t, root, branch, "s1", []any{
			map[string]any{"name": "review", "status": "completed"},
			map[string]any{"name": "pr", "status": "failed"},
		}, nil)

		out, err := recordUserInput(HookCtx{SessionID: "s1"}, Event{Raw: map[string]any{
			"prompt_text": "why did it fail",
		}})
		if err != nil {
			t.Fatal(err)
		}
		assertSilent(t, out)

		entries := readUserInputLines(t, root)
		if len(entries) != 1 {
			t.Fatalf("got %d entries, want 1", len(entries))
		}
		if entries[0].Step != "pr" {
			t.Errorf("Step = %q, want pr", entries[0].Step)
		}
	})

	t.Run("ship, every step completed: run is over, nothing written", func(t *testing.T) {
		root := gitFixture(t, "feat/ui-ship-done")
		branch := "feat/ui-ship-done"
		newShipState(t, root, branch, "s1", []any{
			map[string]any{"name": "review", "status": "completed"},
		}, nil)

		out, err := recordUserInput(HookCtx{SessionID: "s1"}, Event{Raw: map[string]any{
			"prompt_text": "anything",
		}})
		if err != nil {
			t.Fatal(err)
		}
		assertSilent(t, out)
		assertNoUserInputFile(t, root)
	})

	t.Run("execute, no runStatus, wave 2 recorded: records pipeline execute wave 2", func(t *testing.T) {
		root := gitFixture(t, "feat/ui-execute-active")
		branch := "feat/ui-execute-active"
		est, err := state.Init(root, "execute", branch, "s1")
		if err != nil {
			t.Fatal(err)
		}
		est.Data["waves"] = []any{map[string]any{"number": 2}}
		if err := state.Write(est); err != nil {
			t.Fatal(err)
		}

		out, err := recordUserInput(HookCtx{SessionID: "s1"}, Event{Raw: map[string]any{
			"prompt_text": "continue",
		}})
		if err != nil {
			t.Fatal(err)
		}
		assertSilent(t, out)

		entries := readUserInputLines(t, root)
		if len(entries) != 1 {
			t.Fatalf("got %d entries, want 1", len(entries))
		}
		if entries[0].Pipeline != "execute" {
			t.Errorf("Pipeline = %q, want execute", entries[0].Pipeline)
		}
		if entries[0].Wave == nil || *entries[0].Wave != 2 {
			t.Errorf("Wave = %v, want pointer to 2", entries[0].Wave)
		}
	})

	t.Run("execute, runStatus completed: nothing written", func(t *testing.T) {
		root := gitFixture(t, "feat/ui-execute-done")
		branch := "feat/ui-execute-done"
		est, err := state.Init(root, "execute", branch, "s1")
		if err != nil {
			t.Fatal(err)
		}
		est.Data["waves"] = []any{map[string]any{"number": 3}}
		est.Data["runStatus"] = "completed"
		if err := state.Write(est); err != nil {
			t.Fatal(err)
		}

		out, err := recordUserInput(HookCtx{SessionID: "s1"}, Event{Raw: map[string]any{
			"prompt_text": "anything",
		}})
		if err != nil {
			t.Fatal(err)
		}
		assertSilent(t, out)
		assertNoUserInputFile(t, root)
	})

	t.Run("no ship or execute state for branch: nothing written, no file created", func(t *testing.T) {
		root := gitFixture(t, "feat/ui-no-state")

		out, err := recordUserInput(HookCtx{SessionID: "s1"}, Event{Raw: map[string]any{
			"prompt_text": "anything",
		}})
		if err != nil {
			t.Fatal(err)
		}
		assertSilent(t, out)
		assertNoUserInputFile(t, root)
	})

	t.Run("blank prompt (whitespace only): nothing written even with an active ship state", func(t *testing.T) {
		root := gitFixture(t, "feat/ui-blank-prompt")
		branch := "feat/ui-blank-prompt"
		newShipState(t, root, branch, "s1", []any{
			map[string]any{"name": "review", "status": "in_progress"},
		}, nil)

		out, err := recordUserInput(HookCtx{SessionID: "s1"}, Event{Raw: map[string]any{
			"prompt_text": "   ",
		}})
		if err != nil {
			t.Fatal(err)
		}
		assertSilent(t, out)
		assertNoUserInputFile(t, root)
	})

	t.Run("no stdin / no Raw at all: nothing written", func(t *testing.T) {
		root := gitFixture(t, "feat/ui-no-raw")
		branch := "feat/ui-no-raw"
		newShipState(t, root, branch, "s1", []any{
			map[string]any{"name": "review", "status": "in_progress"},
		}, nil)

		out, err := recordUserInput(HookCtx{SessionID: "s1"}, Event{})
		if err != nil {
			t.Fatal(err)
		}
		assertSilent(t, out)
		assertNoUserInputFile(t, root)
	})

	t.Run("secret in the prompt is redacted end to end", func(t *testing.T) {
		root := gitFixture(t, "feat/ui-redact")
		branch := "feat/ui-redact"
		newShipState(t, root, branch, "s1", []any{
			map[string]any{"name": "review", "status": "in_progress"},
		}, nil)

		out, err := recordUserInput(HookCtx{SessionID: "s1"}, Event{Raw: map[string]any{
			"prompt_text": "use Bearer abc.def to call the API",
		}})
		if err != nil {
			t.Fatal(err)
		}
		assertSilent(t, out)

		entries := readUserInputLines(t, root)
		if len(entries) != 1 {
			t.Fatalf("got %d entries, want 1", len(entries))
		}
		if !strings.Contains(entries[0].Text, "Bearer [REDACTED]") {
			t.Errorf("Text = %q, want it to contain Bearer [REDACTED]", entries[0].Text)
		}
	})
}

// TestRun_RecordUserInput exercises the full Run() dispatch path (reading
// stdin JSON, invoking the registered handler, writing stdout) rather than
// calling recordUserInput directly: UserPromptSubmit must never write
// anything to stdout, since any stdout on that event is added to Claude's
// context.
func TestRun_RecordUserInput(t *testing.T) {
	t.Run("active ship state: exit 0, empty stdout, entry recorded", func(t *testing.T) {
		root := gitFixture(t, "feat/ui-run-active")
		branch := "feat/ui-run-active"
		newShipState(t, root, branch, "s1", []any{
			map[string]any{"name": "review", "status": "in_progress"},
		}, nil)

		stdin := `{"hook_event_name":"UserPromptSubmit","prompt_text":"skip the low findings"}`
		var out bytes.Buffer
		code := Run("record-user-input", strings.NewReader(stdin), &out)
		if code != 0 {
			t.Errorf("exit code = %d, want 0", code)
		}
		if out.Len() != 0 {
			t.Errorf("stdout = %q, want empty", out.String())
		}

		entries := readUserInputLines(t, root)
		if len(entries) != 1 {
			t.Fatalf("got %d entries, want 1", len(entries))
		}
	})

	t.Run("no state for branch: exit 0, empty stdout, no file", func(t *testing.T) {
		root := gitFixture(t, "feat/ui-run-no-state")

		stdin := `{"hook_event_name":"UserPromptSubmit","prompt_text":"anything"}`
		var out bytes.Buffer
		code := Run("record-user-input", strings.NewReader(stdin), &out)
		if code != 0 {
			t.Errorf("exit code = %d, want 0", code)
		}
		if out.Len() != 0 {
			t.Errorf("stdout = %q, want empty", out.String())
		}
		assertNoUserInputFile(t, root)
	})

	t.Run("blank stdin: exit 0, empty stdout", func(t *testing.T) {
		var out bytes.Buffer
		code := Run("record-user-input", strings.NewReader(""), &out)
		if code != 0 {
			t.Errorf("exit code = %d, want 0", code)
		}
		if out.Len() != 0 {
			t.Errorf("stdout = %q, want empty", out.String())
		}
	})
}
