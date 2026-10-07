package hooks

import (
	"bytes"
	"strings"
	"testing"

	"github.com/rnagrodzki/sdlc-plugin/internal/state"
	"github.com/rnagrodzki/sdlc-plugin/internal/tools"
)

func TestRecordUserAnswer(t *testing.T) {
	t.Run("one answered question with a header: records kind answer with header: answer text", func(t *testing.T) {
		root := gitFixture(t, "feat/ui-answer-ship-in-progress")
		branch := "feat/ui-answer-ship-in-progress"
		newShipState(t, root, branch, "s1", []any{
			map[string]any{"name": "review", "status": "in_progress"},
		}, nil)

		out, err := recordUserAnswer(HookCtx{SessionID: "s1"}, Event{
			ToolName: "AskUserQuestion",
			Raw: map[string]any{
				"tool_input": map[string]any{
					"questions": []any{
						map[string]any{"header": "OpenSpec", "question": "How …?"},
					},
					"answers": map[string]any{
						"How …?": "Skip OpenSpec",
					},
				},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		assertSilent(t, out)

		entries := readUserInputLines(t, root)
		if len(entries) != 1 {
			t.Fatalf("got %d entries, want 1", len(entries))
		}
		e := entries[0]
		if e.Kind != tools.UserInputKindAnswer {
			t.Errorf("Kind = %q, want %q", e.Kind, tools.UserInputKindAnswer)
		}
		if e.Step != "review" {
			t.Errorf("Step = %q, want review", e.Step)
		}
		if e.Text != "OpenSpec: Skip OpenSpec" {
			t.Errorf("Text = %q, want %q", e.Text, "OpenSpec: Skip OpenSpec")
		}
		if e.Timestamp == "" {
			t.Error("Timestamp is empty, want a value")
		}
	})

	t.Run("question with no header: text falls back to the question text", func(t *testing.T) {
		root := gitFixture(t, "feat/ui-answer-no-header")
		branch := "feat/ui-answer-no-header"
		newShipState(t, root, branch, "s1", []any{
			map[string]any{"name": "pr", "status": "in_progress"},
		}, nil)

		out, err := recordUserAnswer(HookCtx{SessionID: "s1"}, Event{
			ToolName: "AskUserQuestion",
			Raw: map[string]any{
				"tool_input": map[string]any{
					"questions": []any{
						map[string]any{"question": "Which base?"},
					},
					"answers": map[string]any{
						"Which base?": "main",
					},
				},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		assertSilent(t, out)

		entries := readUserInputLines(t, root)
		if len(entries) != 1 {
			t.Fatalf("got %d entries, want 1", len(entries))
		}
		if entries[0].Text != "Which base?: main" {
			t.Errorf("Text = %q, want %q", entries[0].Text, "Which base?: main")
		}
	})

	t.Run("answers only in tool_response: still recorded", func(t *testing.T) {
		root := gitFixture(t, "feat/ui-answer-tool-response")
		branch := "feat/ui-answer-tool-response"
		newShipState(t, root, branch, "s1", []any{
			map[string]any{"name": "review", "status": "in_progress"},
		}, nil)

		out, err := recordUserAnswer(HookCtx{SessionID: "s1"}, Event{
			ToolName: "AskUserQuestion",
			Raw: map[string]any{
				"tool_input": map[string]any{
					"questions": []any{
						map[string]any{"header": "Scope", "question": "Proceed?"},
					},
				},
			},
			ToolResponse: map[string]any{
				"answers": map[string]any{
					"Proceed?": "Yes",
				},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		assertSilent(t, out)

		entries := readUserInputLines(t, root)
		if len(entries) != 1 {
			t.Fatalf("got %d entries, want 1", len(entries))
		}
		if entries[0].Text != "Scope: Yes" {
			t.Errorf("Text = %q, want %q", entries[0].Text, "Scope: Yes")
		}
	})

	t.Run("no answers in tool_input or tool_response: nothing written", func(t *testing.T) {
		root := gitFixture(t, "feat/ui-answer-none")
		branch := "feat/ui-answer-none"
		newShipState(t, root, branch, "s1", []any{
			map[string]any{"name": "review", "status": "in_progress"},
		}, nil)

		out, err := recordUserAnswer(HookCtx{SessionID: "s1"}, Event{
			ToolName: "AskUserQuestion",
			Raw: map[string]any{
				"tool_input": map[string]any{
					"questions": []any{
						map[string]any{"header": "Scope", "question": "Proceed?"},
					},
				},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		assertSilent(t, out)
		assertNoUserInputFile(t, root)
	})

	t.Run("no active run: nothing written, no file created", func(t *testing.T) {
		root := gitFixture(t, "feat/ui-answer-no-state")

		out, err := recordUserAnswer(HookCtx{SessionID: "s1"}, Event{
			ToolName: "AskUserQuestion",
			Raw: map[string]any{
				"tool_input": map[string]any{
					"questions": []any{
						map[string]any{"header": "Scope", "question": "Proceed?"},
					},
					"answers": map[string]any{
						"Proceed?": "Yes",
					},
				},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		assertSilent(t, out)
		assertNoUserInputFile(t, root)
	})

	t.Run("ship, every step completed: run is over, nothing written", func(t *testing.T) {
		root := gitFixture(t, "feat/ui-answer-ship-done")
		branch := "feat/ui-answer-ship-done"
		newShipState(t, root, branch, "s1", []any{
			map[string]any{"name": "review", "status": "completed"},
		}, nil)

		out, err := recordUserAnswer(HookCtx{SessionID: "s1"}, Event{
			ToolName: "AskUserQuestion",
			Raw: map[string]any{
				"tool_input": map[string]any{
					"questions": []any{
						map[string]any{"header": "Scope", "question": "Proceed?"},
					},
					"answers": map[string]any{
						"Proceed?": "Yes",
					},
				},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		assertSilent(t, out)
		assertNoUserInputFile(t, root)
	})

	t.Run("execute, wave 2 recorded: records pipeline execute wave 2", func(t *testing.T) {
		root := gitFixture(t, "feat/ui-answer-execute-active")
		branch := "feat/ui-answer-execute-active"
		est, err := state.Init(root, "execute", branch, "s1")
		if err != nil {
			t.Fatal(err)
		}
		est.Data["waves"] = []any{map[string]any{"number": 2}}
		if err := state.Write(est); err != nil {
			t.Fatal(err)
		}

		out, err := recordUserAnswer(HookCtx{SessionID: "s1"}, Event{
			ToolName: "AskUserQuestion",
			Raw: map[string]any{
				"tool_input": map[string]any{
					"questions": []any{
						map[string]any{"header": "Scope", "question": "Proceed?"},
					},
					"answers": map[string]any{
						"Proceed?": "Yes",
					},
				},
			},
		})
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

	t.Run("blank answer text: that question's line is skipped", func(t *testing.T) {
		root := gitFixture(t, "feat/ui-answer-blank")
		branch := "feat/ui-answer-blank"
		newShipState(t, root, branch, "s1", []any{
			map[string]any{"name": "review", "status": "in_progress"},
		}, nil)

		out, err := recordUserAnswer(HookCtx{SessionID: "s1"}, Event{
			ToolName: "AskUserQuestion",
			Raw: map[string]any{
				"tool_input": map[string]any{
					"questions": []any{
						map[string]any{"header": "Scope", "question": "Proceed?"},
					},
					"answers": map[string]any{
						"Proceed?": "   ",
					},
				},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		assertSilent(t, out)
		assertNoUserInputFile(t, root)
	})

	t.Run("secret in an answer is redacted end to end", func(t *testing.T) {
		root := gitFixture(t, "feat/ui-answer-redact")
		branch := "feat/ui-answer-redact"
		newShipState(t, root, branch, "s1", []any{
			map[string]any{"name": "review", "status": "in_progress"},
		}, nil)

		out, err := recordUserAnswer(HookCtx{SessionID: "s1"}, Event{
			ToolName: "AskUserQuestion",
			Raw: map[string]any{
				"tool_input": map[string]any{
					"questions": []any{
						map[string]any{"header": "Token", "question": "Which token?"},
					},
					"answers": map[string]any{
						"Which token?": "use Bearer abc.def",
					},
				},
			},
		})
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

// TestRun_RecordUserAnswer exercises the full Run() dispatch path (reading
// stdin JSON, invoking the registered handler, writing stdout): a
// PostToolUse hook must still write nothing to stdout here, matching
// recordUserAnswer's own silent-on-every-path contract.
func TestRun_RecordUserAnswer(t *testing.T) {
	t.Run("active ship state: exit 0, empty stdout, entry recorded", func(t *testing.T) {
		root := gitFixture(t, "feat/ui-answer-run-active")
		branch := "feat/ui-answer-run-active"
		newShipState(t, root, branch, "s1", []any{
			map[string]any{"name": "review", "status": "in_progress"},
		}, nil)

		stdin := `{"hook_event_name":"PostToolUse","tool_name":"AskUserQuestion","tool_input":{"questions":[{"header":"OpenSpec","question":"How …?"}],"answers":{"How …?":"Skip OpenSpec"}}}`
		var out bytes.Buffer
		code := Run("record-user-answer", strings.NewReader(stdin), &out)
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
		root := gitFixture(t, "feat/ui-answer-run-no-state")

		stdin := `{"hook_event_name":"PostToolUse","tool_name":"AskUserQuestion","tool_input":{"questions":[{"header":"OpenSpec","question":"How …?"}],"answers":{"How …?":"Skip OpenSpec"}}}`
		var out bytes.Buffer
		code := Run("record-user-answer", strings.NewReader(stdin), &out)
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
		code := Run("record-user-answer", strings.NewReader(""), &out)
		if code != 0 {
			t.Errorf("exit code = %d, want 0", code)
		}
		if out.Len() != 0 {
			t.Errorf("stdout = %q, want empty", out.String())
		}
	})
}
