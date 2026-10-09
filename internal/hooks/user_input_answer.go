package hooks

import (
	"fmt"
	"strings"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/attention"
	"github.com/rnagrodzki/sdlc-plugin/internal/tools"
)

// recordUserAnswer is the "record-user-answer" hook handler (PostToolUse,
// matcher AskUserQuestion). It appends one user-input entry per answered
// question to user-inputs.jsonl (kind "answer") when a ship or execute run
// is active on this branch — the AskUserQuestion-side counterpart to
// recordUserInput (user_input_record.go). It shares that handler's active-run
// rule (userInputRunContext) and evidence sink (tools.AppendUserInput), so
// the same ship/execute attribution and run-is-over cutoffs apply to
// question answers as to typed prompts. It always returns Output{}: a
// PostToolUse hook has nothing advisory to say back, and this hook must stay
// silent on every path — recorded, dropped, or errored — per spec.
//
// Its first call is closeQuestionWait, which deletes the question wait record
// of this call, so the record is gone before any early return.
func recordUserAnswer(ctx HookCtx, event Event) (Output, error) {
	silent := Output{}
	closeQuestionWait(ctx, event)

	toolInput, _ := event.Raw["tool_input"].(map[string]any)

	answers, _ := toolInput["answers"].(map[string]any)
	if answers == nil {
		answers, _ = event.ToolResponse["answers"].(map[string]any)
	}
	if len(answers) == 0 {
		return silent, nil
	}

	questions, _ := toolInput["questions"].([]any)
	if len(questions) == 0 {
		return silent, nil
	}

	root, branch, ok := resolveRootBranch()
	if !ok {
		return silent, nil
	}

	pipelineName, step, wave, active := userInputRunContext(root, branch)
	if !active {
		return silent, nil
	}

	for _, raw := range questions {
		q, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		questionText, _ := q["question"].(string)
		if questionText == "" {
			continue
		}
		answer, ok := answers[questionText].(string)
		if !ok || strings.TrimSpace(answer) == "" {
			continue
		}

		header, _ := q["header"].(string)
		label := header
		if label == "" {
			label = questionText
		}

		// Fire-and-forget: a write failure must never affect this hook's
		// return value (always silent) or the calling tool's own result.
		_ = tools.AppendUserInput(root, tools.UserInputEntry{
			Timestamp: time.Now().UTC().Format(time.RFC3339),
			Pipeline:  pipelineName,
			Step:      step,
			Wave:      wave,
			Branch:    branch,
			Text:      fmt.Sprintf("%s: %s", label, answer),
			Kind:      tools.UserInputKindAnswer,
		})
	}

	return silent, nil
}

// closeQuestionWait deletes the question attention record of the
// AskUserQuestion call in event (keyed by tool_use_id). With no tool_use_id it
// deletes every question record of the session. It does nothing when the
// session ID is empty or the root does not resolve, and it drops a delete
// error: the hook output never changes.
func closeQuestionWait(ctx HookCtx, event Event) {
	if ctx.SessionID == "" {
		return
	}
	root, err := mainRootFunc()
	if err != nil {
		return
	}
	toolUseID, _ := event.Raw["tool_use_id"].(string)
	_ = attention.DeleteQuestion(root, ctx.SessionID, toolUseID)
}
