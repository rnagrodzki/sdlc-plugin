package tools

import (
	"path/filepath"
	"regexp"
	"strings"

	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
	"github.com/rnagrodzki/sdlc-plugin/internal/telemetry"
)

// UserInputEntry is one prompt the user typed, or one question answer the
// user gave, while a ship or execute run was active on the branch.
type UserInputEntry struct {
	Timestamp string `json:"ts"`
	Pipeline  string `json:"pipeline"`       // "ship" or "execute"
	Step      string `json:"step,omitempty"` // ship step name
	Wave      *int   `json:"wave,omitempty"` // execute wave number
	Branch    string `json:"branch"`
	Text      string `json:"text"`           // redacted, max userInputTextMax runes
	Kind      string `json:"kind,omitempty"` // "prompt" or "answer"; a line with no kind is read as "prompt"
	SessionID string `json:"sessionId"`      // Claude Code session id; "" when unknown, but always present
}

const (
	// userInputTextMax bounds each stored prompt to this many runes, with a
	// trailing "…" marking truncation when the prompt is longer — mirrors
	// CLIEvidenceEntry.OutputHead's "first ~500 chars of output" cap, scaled
	// up since a user-typed prompt (not command output) is the payload here.
	userInputTextMax = 2000

	// maxUserInputInWindow is the default cap for readUserInputInWindow,
	// mirroring maxCLIEvidenceInWindow's caller-provided cap pattern.
	maxUserInputInWindow = 100

	// UserInputKindPrompt marks a UserInputEntry recorded from a typed
	// prompt. An entry with no Kind (written before this field existed) is
	// read as this kind too.
	UserInputKindPrompt = "prompt"

	// UserInputKindAnswer marks a UserInputEntry recorded from an answered
	// AskUserQuestion.
	UserInputKindAnswer = "answer"
)

// userInputPath returns the path to the user-input evidence JSONL file.
func userInputPath(root string) string {
	return filepath.Join(root, paths.DataDir, paths.EvidenceSubdir, "user-inputs.jsonl")
}

// userInputDropTags lists the envelope tags that mark a whole turn as
// injected by the editor or the agent runtime, never something the user
// typed. When the first non-blank text of a prompt starts with one of
// these, the entire turn is dropped: nothing is recorded.
var userInputDropTags = []string{
	"<task-notification>",
	"<local-command-stdout>",
	"<local-command-stderr>",
	"<local-command-caveat>",
}

// userInputStripTags lists the envelope tags that can appear inside an
// otherwise-genuine typed prompt (an IDE or a system-reminder notice stitched
// in by the agent runtime). Each occurrence, wherever it appears and however
// many lines it spans, is removed; the surrounding text is what the user
// actually typed.
var userInputStripTags = []*regexp.Regexp{
	regexp.MustCompile(`(?s)<ide_opened_file>.*?</ide_opened_file>`),
	regexp.MustCompile(`(?s)<ide_selection>.*?</ide_selection>`),
	regexp.MustCompile(`(?s)<system-reminder>.*?</system-reminder>`),
}

// slashCommandPattern matches the envelope the agent runtime wraps a typed
// slash command invocation in: <command-name>/<name></command-name>
// <command-args><args></command-args>. Capture group 1 is the command name
// (leading "/" included), group 2 is the raw args text.
var slashCommandPattern = regexp.MustCompile(`(?s)<command-name>(.*?)</command-name><command-args>(.*?)</command-args>`)

// CleanUserPrompt classifies a raw UserPromptSubmit (or AskUserQuestion
// answer) text before it is recorded as user-input evidence, so an injected
// turn never displaces a real typed prompt out of the report's bounded
// window. Returns the cleaned text and whether it should be recorded at
// all — false means drop the turn; nothing is written.
//
//   - A turn whose first non-blank text starts with a userInputDropTags
//     entry is dropped outright (not something the user typed).
//   - A slash-command envelope (slashCommandPattern) is collapsed to
//     "/<name> <args>".
//   - Each userInputStripTags match is removed, wherever it appears.
//   - When collapsing or stripping leaves nothing behind, the turn is
//     dropped. Otherwise the result is trimmed and recorded.
//   - Text matching none of the above (including an unknown tag) is
//     recorded unchanged.
func CleanUserPrompt(text string) (string, bool) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return "", false
	}

	for _, tag := range userInputDropTags {
		if strings.HasPrefix(trimmed, tag) {
			return "", false
		}
	}

	cleaned := text
	changed := false

	if loc := slashCommandPattern.FindStringSubmatchIndex(cleaned); loc != nil {
		full := cleaned[loc[0]:loc[1]]
		name := cleaned[loc[2]:loc[3]]
		args := strings.TrimSpace(cleaned[loc[4]:loc[5]])
		replacement := name
		if args != "" {
			replacement = name + " " + args
		}
		cleaned = strings.Replace(cleaned, full, replacement, 1)
		changed = true
	}

	for _, tag := range userInputStripTags {
		if tag.MatchString(cleaned) {
			cleaned = tag.ReplaceAllString(cleaned, "")
			changed = true
		}
	}

	if changed {
		cleaned = strings.TrimSpace(cleaned)
		if cleaned == "" {
			return "", false
		}
	}

	return cleaned, true
}

// truncateRunes cuts s to at most max runes, appending a trailing "…" when
// truncation actually happens. Operates on runes (not bytes) so a multi-byte
// character is never split mid-sequence.
func truncateRunes(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "…"
}

// appendUserInput redacts entry.Text (telemetry.Redact) and caps it to
// userInputTextMax runes (truncateRunes), then appends it through
// appendJSONLBounded (cli_evidence.go) — the same size-capped,
// rotate-on-overflow JSONL append path CLI evidence uses.
func appendUserInput(root string, entry UserInputEntry) error {
	entry.Text = truncateRunes(telemetry.Redact(entry.Text), userInputTextMax)
	return appendJSONLBounded(userInputPath(root), entry)
}

// readAllUserInput reads and parses every entry from the user-input
// evidence JSONL file. Returns (nil, nil) when the file does not exist.
// Malformed lines are silently skipped.
func readAllUserInput(root string) ([]UserInputEntry, error) {
	return readJSONLEntries[UserInputEntry](userInputPath(root), "user input evidence")
}

// readUserInputInWindow reads entries from the user-input evidence JSONL
// file matching the given branch whose timestamp is at or after since. Same
// filter and tail rule as readCLIEvidenceInWindow (cli_evidence.go): entries
// are matched oldest-first in file order, then capped to the last n (tail)
// when more than n match. Returns an empty slice (never nil) and a nil error
// when the file is missing or empty, or when nothing matches.
func readUserInputInWindow(root, branch, since string, n int) ([]UserInputEntry, error) {
	entries, err := readAllUserInput(root)
	if err != nil {
		return nil, err
	}

	var matched []UserInputEntry
	for _, entry := range entries {
		if entry.Branch != branch {
			continue
		}
		if entry.Timestamp < since {
			continue
		}
		matched = append(matched, entry)
	}

	if matched == nil {
		return []UserInputEntry{}, nil
	}

	if len(matched) > n {
		matched = matched[len(matched)-n:]
	}

	return matched, nil
}

// ---------------------------------------------------------------------------
// Exported wrapper -- internal/hooks needs to call this directly from its
// UserPromptSubmit handler (record_user_input.go), but Go visibility makes
// the unexported appendUserInput above uncallable cross-package.
// ---------------------------------------------------------------------------

// AppendUserInput delegates to appendUserInput for internal/hooks.
func AppendUserInput(root string, entry UserInputEntry) error {
	return appendUserInput(root, entry)
}
