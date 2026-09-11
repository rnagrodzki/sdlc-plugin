package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
	"github.com/rnagrodzki/sdlc-plugin/internal/worktree"
)

// ---------------------------------------------------------------------------
// learnings_log tool
// ---------------------------------------------------------------------------

// learningsLogHeader is written once, when the log file does not yet exist.
const learningsLogHeader = "# SDLC Execution Learnings\n"

// LearningsLogIn is the input for the learnings_log tool.
type LearningsLogIn struct {
	// Action selects the operation: "append", "read", or "remove".
	Action string `json:"action" jsonschema:"enum=append,enum=read,enum=remove" jsonschema_description:"Selects the operation: \"append\", \"read\", or \"remove\"."`
	// Entry is the markdown block to append (required for "append"). It is
	// written verbatim, separated from surrounding content by one blank
	// line; do not include a leading or trailing blank line. Must not
	// contain a blank line ("\n\n") — that is the entry delimiter.
	Entry string `json:"entry,omitempty" jsonschema_description:"Markdown block to append (required for action \"append\"). Written verbatim, separated from surrounding content by one blank line; do not include a leading or trailing blank line. Must not contain a blank line (the entry delimiter)."`
	// TailLines, for "read", limits the returned content to the last N
	// lines. Zero (default) returns the whole file.
	TailLines int `json:"tailLines,omitempty" jsonschema_description:"For action \"read\", limits the returned content to the last N lines. Zero (default) returns the whole file."`
	// Indices, for "remove", selects which entries to delete.
	Indices []int `json:"indices,omitempty" jsonschema_description:"1-indexed entry numbers to remove (required for action \"remove\"). Entries are blocks separated by blank lines, header excluded."`
}

// LearningsLogOut is the output for the learnings_log tool.
type LearningsLogOut struct {
	OK      bool   `json:"ok"`
	Action  string `json:"action"`
	Path    string `json:"path"`
	Exists  bool   `json:"exists"`
	Changed bool   `json:"changed"`
	Content string `json:"content,omitempty"`
	Next    string `json:"next"`
}

// learningsLog is the core logic, separated from the handler for testability.
// root is always the MAIN worktree root (resolved by the registered handler)
// so that entries written from a feature worktree still land in the one
// persistent log — a feature worktree's local .sdlc-v2/learnings/ is never
// git-tracked and is lost the moment the worktree is removed.
func learningsLog(root string, in LearningsLogIn) (LearningsLogOut, error) {
	rel := paths.DataDir + "/learnings/log.md"
	path := filepath.Join(root, paths.DataDir, "learnings", "log.md")

	switch in.Action {
	case "append":
		return learningsAppend(path, rel, in.Entry)
	case "read":
		return learningsRead(path, rel, in.TailLines)
	case "remove":
		return learningsRemove(path, rel, in.Indices)
	default:
		return LearningsLogOut{}, &mcpserver.DomainError{
			Msg:        fmt.Sprintf("unknown learnings_log action %q; must be one of: append, read, remove", in.Action),
			Suggestion: "Set action to one of: \"append\", \"read\", \"remove\".",
		}
	}
}

func learningsAppend(path, rel, entry string) (LearningsLogOut, error) {
	entry = strings.TrimSpace(entry)
	if entry == "" {
		return LearningsLogOut{}, &mcpserver.DomainError{
			Msg:        "entry must not be empty",
			Suggestion: "Provide a non-empty markdown block as the entry.",
		}
	}
	if strings.Contains(entry, "\n\n") {
		return LearningsLogOut{}, &mcpserver.DomainError{
			Msg:        "entry must not contain a blank line (\"\\n\\n\"); blank lines delimit entries in the log",
			Suggestion: "Use single newlines within an entry. Split multi-paragraph content into separate append calls, or join paragraphs with a single newline.",
		}
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return LearningsLogOut{}, &mcpserver.InfraError{
			Msg:   fmt.Sprintf("create learnings directory: %s", err.Error()),
			Cause: err,
		}
	}

	existing, err := os.ReadFile(path)
	exists := err == nil
	if err != nil && !os.IsNotExist(err) {
		return LearningsLogOut{}, &mcpserver.InfraError{
			Msg:   fmt.Sprintf("read learnings log: %s", err.Error()),
			Cause: err,
		}
	}

	var out strings.Builder
	if exists {
		out.Write(existing)
		if !strings.HasSuffix(string(existing), "\n") {
			out.WriteString("\n")
		}
		out.WriteString("\n")
	} else {
		out.WriteString(learningsLogHeader)
		out.WriteString("\n")
	}
	out.WriteString(entry)
	out.WriteString("\n")

	if err := os.WriteFile(path, []byte(out.String()), 0o644); err != nil {
		return LearningsLogOut{}, &mcpserver.InfraError{
			Msg:   fmt.Sprintf("write learnings log: %s", err.Error()),
			Cause: err,
		}
	}

	return LearningsLogOut{
		OK:      true,
		Action:  "append",
		Path:    rel,
		Exists:  true,
		Changed: true,
		Next:    "Call action=\"read\" to see the full log.",
	}, nil
}

func learningsRead(path, rel string, tailLines int) (LearningsLogOut, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return LearningsLogOut{OK: true, Action: "read", Path: rel, Exists: false}, nil
		}
		return LearningsLogOut{}, &mcpserver.InfraError{
			Msg:   fmt.Sprintf("read learnings log: %s", err.Error()),
			Cause: err,
		}
	}

	content := string(data)
	if tailLines > 0 {
		lines := strings.Split(strings.TrimSuffix(content, "\n"), "\n")
		if len(lines) > tailLines {
			lines = lines[len(lines)-tailLines:]
		}
		content = strings.Join(lines, "\n")
	}

	return LearningsLogOut{
		OK:      true,
		Action:  "read",
		Path:    rel,
		Exists:  true,
		Content: content,
	}, nil
}

func learningsRemove(path, rel string, indices []int) (LearningsLogOut, error) {
	if len(indices) == 0 {
		return LearningsLogOut{}, &mcpserver.DomainError{
			Msg:        "indices must not be empty for action \"remove\"",
			Suggestion: "Pass a non-empty indices array with 1-indexed entry numbers to remove.",
		}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return LearningsLogOut{}, &mcpserver.DomainError{
				Msg:        "learnings log does not exist; nothing to remove",
				Suggestion: "Call action=\"read\" first to confirm the log exists before attempting removal.",
			}
		}
		return LearningsLogOut{}, &mcpserver.InfraError{
			Msg:   fmt.Sprintf("read learnings log: %s", err.Error()),
			Cause: err,
		}
	}

	// Entries are blocks separated by a blank line ("\n\n"); the first block
	// is the header and is not a removable entry.
	blocks := strings.Split(string(data), "\n\n")
	header := blocks[0]
	entries := blocks[1:]
	if len(entries) == 0 {
		return LearningsLogOut{}, &mcpserver.DomainError{
			Msg:        "learnings log has no entries to remove",
			Suggestion: "Call action=\"read\" to verify the log has entries before attempting removal.",
		}
	}

	remove := make(map[int]bool, len(indices))
	for _, idx := range indices {
		if idx < 1 || idx > len(entries) {
			return LearningsLogOut{}, &mcpserver.DomainError{
				Msg:        fmt.Sprintf("index %d out of range; log has %d entries", idx, len(entries)),
				Suggestion: "Call action=\"read\" to see current entries, then retry with valid 1-indexed entry numbers.",
			}
		}
		remove[idx] = true
	}

	kept := make([]string, 0, len(entries))
	removed := make([]string, 0, len(indices))
	for i, entry := range entries {
		if remove[i+1] {
			removed = append(removed, strings.TrimRight(entry, "\n"))
			continue
		}
		kept = append(kept, strings.TrimRight(entry, "\n"))
	}

	var out strings.Builder
	out.WriteString(strings.TrimRight(header, "\n"))
	out.WriteString("\n")
	if len(kept) > 0 {
		out.WriteString("\n")
		out.WriteString(strings.Join(kept, "\n\n"))
		out.WriteString("\n")
	}

	if err := os.WriteFile(path, []byte(out.String()), 0o644); err != nil {
		return LearningsLogOut{}, &mcpserver.InfraError{
			Msg:   fmt.Sprintf("write learnings log: %s", err.Error()),
			Cause: err,
		}
	}

	return LearningsLogOut{
		OK:      true,
		Action:  "remove",
		Path:    rel,
		Exists:  true,
		Changed: true,
		Content: strings.Join(removed, "\n\n"),
		Next:    "Call action=\"read\" to verify the updated log contents.",
	}, nil
}

// ---------------------------------------------------------------------------
// Registration
// ---------------------------------------------------------------------------

// RegisterLearningsTools registers the learnings_log tool on the server.
func RegisterLearningsTools(s *mcpserver.Server) {
	mcpserver.Register(s, "learnings_log",
		"Appends to, reads, or removes entries from "+paths.DataDir+"/learnings/log.md. Always resolves the MAIN git worktree root first (worktree.MainRoot, falling back to cwd) — a feature worktree's own copy of this file is never git-tracked and is lost when that worktree is removed, so every skill must go through this tool instead of Read/Edit-ing the file directly at the current worktree's path. action=\"append\" (entry: markdown block, no leading/trailing blank line, must not contain a blank line — that is the entry delimiter) adds it as a new entry separated by one blank line, creating the file with its standard header on first use. action=\"read\" (optional tailLines) returns the current content, or exists=false when nothing has been logged yet. action=\"remove\" (indices: 1-indexed list of entry numbers, header excluded) deletes the specified entries, echoes the removed content in the response, and rewrites the file.",
		func(ctx mcpserver.Ctx, in LearningsLogIn) (LearningsLogOut, error) {
			root, err := worktree.MainRoot()
			if err != nil {
				root, err = os.Getwd()
				if err != nil {
					return LearningsLogOut{}, &mcpserver.InfraError{
						Msg:   fmt.Sprintf("resolve project root: %s", err.Error()),
						Cause: err,
					}
				}
			}
			return learningsLog(root, in)
		},
	)
}
