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
	// Action selects the operation: "append" or "read".
	Action string `json:"action"`
	// Entry is the markdown block to append (required for "append"). It is
	// written verbatim, separated from surrounding content by one blank
	// line; do not include a leading or trailing blank line.
	Entry string `json:"entry"`
	// TailLines, for "read", limits the returned content to the last N
	// lines. Zero (default) returns the whole file.
	TailLines int `json:"tailLines"`
}

// LearningsLogOut is the output for the learnings_log tool.
type LearningsLogOut struct {
	OK      bool   `json:"ok"`
	Action  string `json:"action"`
	Path    string `json:"path"`
	Exists  bool   `json:"exists"`
	Changed bool   `json:"changed"`
	Content string `json:"content,omitempty"`
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
	default:
		return LearningsLogOut{}, &mcpserver.DomainError{
			Msg: fmt.Sprintf("unknown learnings_log action %q; must be one of: append, read", in.Action),
		}
	}
}

func learningsAppend(path, rel, entry string) (LearningsLogOut, error) {
	entry = strings.TrimSpace(entry)
	if entry == "" {
		return LearningsLogOut{}, &mcpserver.DomainError{Msg: "entry must not be empty"}
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

// ---------------------------------------------------------------------------
// Registration
// ---------------------------------------------------------------------------

// RegisterLearningsTools registers the learnings_log tool on the server.
func RegisterLearningsTools(s *mcpserver.Server) {
	mcpserver.Register(s, "learnings_log",
		"Appends to or reads "+paths.DataDir+"/learnings/log.md. Always resolves the MAIN git worktree root first (worktree.MainRoot, falling back to cwd) — a feature worktree's own copy of this file is never git-tracked and is lost when that worktree is removed, so every skill must go through this tool instead of Read/Edit-ing the file directly at the current worktree's path. action=\"append\" (entry: markdown block, no leading/trailing blank line) adds it as a new entry separated by one blank line, creating the file with its standard header on first use. action=\"read\" (optional tailLines) returns the current content, or exists=false when nothing has been logged yet.",
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
