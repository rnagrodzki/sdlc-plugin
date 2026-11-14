package tools

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/rnagrodzki/sdlc-plugin/internal/config"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/worktree"
)

// ---------------------------------------------------------------------------
// instructions_resolve tool
// ---------------------------------------------------------------------------

// InstructionsResolveIn is the input for the instructions_resolve tool.
type InstructionsResolveIn struct {
	Key string `json:"key" jsonschema_description:"Required. Skill key: one of execute, commit, review, received-review, pr, harden, verify-pipeline. Example: pr"`
}

// InstructionsResolveOut is the output of the instructions_resolve tool.
type InstructionsResolveOut struct {
	Key          string               `json:"key"`
	Count        int                  `json:"count"`
	Instructions []config.Instruction `json:"instructions"`
	Summary      string               `json:"summary"`
	Next         string               `json:"next"`
}

// instructionsResolveDescription is the MCP description of instructions_resolve.
const instructionsResolveDescription = "Returns the follow-up instructions of one standalone skill. Call it at the start of execute, commit, review, received-review, pr, harden or verify-pipeline, unless the skill got --no-instructions. Requires: key. Optional: none. Reads the [instructions] section of .sdlc-v2/config.toml (team), .sdlc-v2/local.toml (project local) and the user-level config file, in the main worktree. The team list comes first. The project local list follows when that file sets the key, else the user-level list. An empty project local list hides the user-level list. Returns key, count, instructions (each with key, number, source, text, normalQuestions, criticalQuestions, onQuestionNotAllowed), summary and next. Errors: DomainError for a key that runs only in a ship run, DataError for bad config content, InfraError for a file that cannot be read. Writes nothing."

// instructionsResolve returns the merged instruction list of one standalone
// skill key, read from the config files under root (the main worktree root).
func instructionsResolve(root string, in InstructionsResolveIn) (InstructionsResolveOut, error) {
	if !slices.Contains(config.StandaloneInstructionKeys, in.Key) {
		return InstructionsResolveOut{}, &mcpserver.DomainError{
			Msg:        fmt.Sprintf("%s runs only in a ship run", in.Key),
			Suggestion: "Use one of: " + strings.Join(config.StandaloneInstructionKeys, ", ") + ".",
		}
	}

	lists, err := config.ReadInstructions(root)
	if err != nil {
		var ie *config.InstructionsError
		if errors.As(err, &ie) && ie.Kind == config.InstructionsErrorData {
			return InstructionsResolveOut{}, &mcpserver.DataError{
				Msg:        fmt.Sprintf("%s: config error, no step started.\n%s", in.Key, ie.Error()),
				Suggestion: ie.Fix,
				Cause:      err,
			}
		}
		return InstructionsResolveOut{}, &mcpserver.InfraError{
			Msg:        err.Error(),
			Suggestion: "Make the file readable, then run the skill again.",
			Cause:      err,
		}
	}

	list := lists[in.Key]
	if len(list) == 0 {
		return InstructionsResolveOut{
			Key:          in.Key,
			Count:        0,
			Instructions: []config.Instruction{},
			Summary:      fmt.Sprintf("%s has 0 instructions.", in.Key),
			Next:         fmt.Sprintf("No instructions for %s. Continue the skill.", in.Key),
		}, nil
	}
	return InstructionsResolveOut{
		Key:          in.Key,
		Count:        len(list),
		Instructions: list,
		Summary:      fmt.Sprintf("%s has %d instructions.", in.Key, len(list)),
		Next:         "Run the list after the skill completes with success. Follow plugins/sdlc/skills/ship/instructions-run.md.",
	}, nil
}

// ---------------------------------------------------------------------------
// Registration
// ---------------------------------------------------------------------------

// RegisterInstructionsTools registers the instructions_resolve tool on the server.
func RegisterInstructionsTools(s *mcpserver.Server) {
	mcpserver.Register(s, "instructions_resolve",
		instructionsResolveDescription,
		mcpserver.Annotations{
			Title:      "Resolve skill instructions",
			ReadOnly:   true,
			Idempotent: true,
			OpenWorld:  false,
		},
		func(ctx mcpserver.Ctx, in InstructionsResolveIn) (InstructionsResolveOut, error) {
			// Same root as learnings_log and ship_prepare: a linked worktree
			// reads the config files of the main worktree.
			root, err := worktree.MainRoot()
			if err != nil {
				root, err = os.Getwd()
				if err != nil {
					return InstructionsResolveOut{}, &mcpserver.InfraError{
						Msg:        fmt.Sprintf("resolve project root: %s", err.Error()),
						Suggestion: "Run the skill inside a git repository.",
						Cause:      err,
					}
				}
			}
			return instructionsResolve(root, in)
		},
	)
}
