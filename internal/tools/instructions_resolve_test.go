package tools

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rnagrodzki/sdlc-plugin/internal/config"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
)

// instructionsResolveRoot makes a root with the given team and project local
// config contents. An empty string writes no file. The user-level file is a
// fresh path that does not exist, unless the test sets its own.
func instructionsResolveRoot(t *testing.T, team, local string) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, ".sdlc-v2")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if team != "" {
		writeFile(t, filepath.Join(dir, "config.toml"), team)
	}
	if local != "" {
		writeFile(t, filepath.Join(dir, "local.toml"), local)
	}
	t.Setenv(config.UserConfigPathEnv, filepath.Join(t.TempDir(), "user-local.toml"))
	return root
}

func TestInstructionsResolve_ListPresent(t *testing.T) {
	root := instructionsResolveRoot(t,
		"[[instructions.pr]]\ntext = \"Post the PR link.\"\n\n[[instructions.review]]\ntext = \"r\"\n",
		"[[instructions.pr]]\ntext = \"Ping the reviewer.\"\nnormalQuestions = true\n",
	)

	got, err := instructionsResolve(root, InstructionsResolveIn{Key: "pr"})
	if err != nil {
		t.Fatalf("instructionsResolve: %v", err)
	}
	if got.Key != "pr" || got.Count != 2 {
		t.Fatalf("key/count = %q/%d, want pr/2", got.Key, got.Count)
	}
	var lines []string
	for _, in := range got.Instructions {
		lines = append(lines, in.Key+" #"+strconv.Itoa(in.Number)+" ("+in.Source+") "+in.Text)
	}
	want := []string{"pr #1 (team) Post the PR link.", "pr #2 (project local) Ping the reviewer."}
	if !reflect.DeepEqual(lines, want) {
		t.Errorf("instructions = %v, want %v", lines, want)
	}
	if !got.Instructions[1].NormalQuestions {
		t.Error("pr #2 normalQuestions = false, want true")
	}
	wantNext := "Run the list after the skill completes with success. Follow plugins/sdlc/skills/ship/instructions-run.md."
	if got.Next != wantNext {
		t.Errorf("next = %q, want %q", got.Next, wantNext)
	}
	if got.Summary == "" {
		t.Error("summary is empty")
	}
}

func TestInstructionsResolve_UserListWhenLocalDoesNotSetKey(t *testing.T) {
	root := instructionsResolveRoot(t, "", "")
	userPath := filepath.Join(t.TempDir(), "user-local.toml")
	writeFile(t, userPath, "[[instructions.commit]]\ntext = \"u\"\n")
	t.Setenv(config.UserConfigPathEnv, userPath)

	got, err := instructionsResolve(root, InstructionsResolveIn{Key: "commit"})
	if err != nil {
		t.Fatalf("instructionsResolve: %v", err)
	}
	if got.Count != 1 || got.Instructions[0].Source != "user" {
		t.Errorf("got %+v, want 1 user instruction", got)
	}
}

func TestInstructionsResolve_NoList(t *testing.T) {
	root := instructionsResolveRoot(t, "[[instructions.pr]]\ntext = \"a\"\n", "")

	got, err := instructionsResolve(root, InstructionsResolveIn{Key: "review"})
	if err != nil {
		t.Fatalf("instructionsResolve: %v", err)
	}
	if got.Count != 0 || len(got.Instructions) != 0 {
		t.Errorf("count/len = %d/%d, want 0/0", got.Count, len(got.Instructions))
	}
	if got.Next != "No instructions for review. Continue the skill." {
		t.Errorf("next = %q", got.Next)
	}
}

// callRegisteredInstructionsResolve calls the registered tool over an
// in-memory MCP session from the current folder and returns the rendered text.
func callRegisteredInstructionsResolve(t *testing.T, key string) string {
	t.Helper()
	s := mcpserver.New("test", "0.0.0-test")
	RegisterInstructionsTools(s)

	ctx := context.Background()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	if _, err := s.MCPServer().Connect(ctx, serverTransport, nil); err != nil {
		t.Fatalf("server Connect: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0.0.0"}, nil)
	c, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client Connect: %v", err)
	}
	t.Cleanup(func() { c.Close() })

	res, err := c.CallTool(ctx, &mcp.CallToolParams{Name: "instructions_resolve", Arguments: map[string]any{"key": key}})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError || len(res.Content) == 0 {
		t.Fatalf("instructions_resolve failed: isError=%v content=%v", res.IsError, res.Content)
	}
	text, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("content[0] is %T, want *mcp.TextContent", res.Content[0])
	}
	return text.Text
}

// TestInstructionsResolve_RegisteredToolReadsMainWorktree runs the registered
// tool in a linked worktree with its own local.toml: the tool reads the files
// of the main worktree, and a key with no list renders the (none) marker.
func TestInstructionsResolve_RegisteredToolReadsMainWorktree(t *testing.T) {
	mainRoot := instructionsResolveRoot(t, "[[instructions.pr]]\ntext = \"Post the PR link.\"\n", "")
	runGit(t, mainRoot, "init", "-q", "-b", "main")
	runGit(t, mainRoot, "add", "-A")
	runGit(t, mainRoot, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "-m", "init")
	linked := filepath.Join(t.TempDir(), "linked")
	runGit(t, mainRoot, "worktree", "add", "-q", "-b", "feat/x", linked)
	writeFile(t, filepath.Join(linked, ".sdlc-v2", "local.toml"), "[[instructions.pr]]\ntext = \"linked only\"\n")
	t.Chdir(linked)

	pr := callRegisteredInstructionsResolve(t, "pr")
	if !strings.Contains(pr, "Post the PR link.") || strings.Contains(pr, "linked only") {
		t.Errorf("pr output does not come from the main worktree only:\n%s", pr)
	}

	review := callRegisteredInstructionsResolve(t, "review")
	if !strings.Contains(review, "(none)") || !strings.Contains(review, "No instructions for review. Continue the skill.") {
		t.Errorf("review output has no (none) marker or next text:\n%s", review)
	}
}

// TestInstructionsResolve_KeyEnumMatchesConfig proves that the schema enum of
// InstructionsResolveIn.Key equals config.StandaloneInstructionKeys, in order.
func TestInstructionsResolve_KeyEnumMatchesConfig(t *testing.T) {
	field, ok := reflect.TypeOf(InstructionsResolveIn{}).FieldByName("Key")
	if !ok {
		t.Fatal("InstructionsResolveIn.Key field not found")
	}
	var enums []string
	for _, part := range strings.Split(field.Tag.Get("jsonschema"), ",") {
		if v, found := strings.CutPrefix(part, "enum="); found {
			enums = append(enums, v)
		}
	}
	if !reflect.DeepEqual(enums, config.StandaloneInstructionKeys) {
		t.Errorf("Key enum = %v, want config.StandaloneInstructionKeys %v", enums, config.StandaloneInstructionKeys)
	}
}

func TestInstructionsResolve_NotStandaloneKey(t *testing.T) {
	root := instructionsResolveRoot(t, "", "")
	for _, key := range []string{"", "commit-fixes", "ship", "deploy"} {
		t.Run("key="+key, func(t *testing.T) {
			_, err := instructionsResolve(root, InstructionsResolveIn{Key: key})
			var de *mcpserver.DomainError
			if !errors.As(err, &de) {
				t.Fatalf("error is %T (%v), want *mcpserver.DomainError", err, err)
			}
			if de.Msg != key+" runs only in a ship run" {
				t.Errorf("Msg = %q", de.Msg)
			}
			wantSug := "Use one of: execute, commit, review, received-review, pr, harden, verify-pipeline."
			if de.Suggestion != wantSug {
				t.Errorf("Suggestion = %q, want %q", de.Suggestion, wantSug)
			}
		})
	}
}

func TestInstructionsResolve_DataError(t *testing.T) {
	root := instructionsResolveRoot(t, "[[instructions.pr]]\ntext = \"a\"\nonQuestionNotAllowed = \"ask\"\n", "")

	_, err := instructionsResolve(root, InstructionsResolveIn{Key: "pr"})
	var de *mcpserver.DataError
	if !errors.As(err, &de) {
		t.Fatalf("error is %T (%v), want *mcpserver.DataError", err, err)
	}
	wantPrefix := "pr: config error, no instruction ran.\nFile: .sdlc-v2/config.toml\nField: instructions.pr[1].onQuestionNotAllowed\n"
	if !strings.HasPrefix(de.Msg, wantPrefix) {
		t.Errorf("Msg = %q, want prefix %q", de.Msg, wantPrefix)
	}
	if de.Suggestion != `use "skip" or "decide".` {
		t.Errorf("Suggestion = %q", de.Suggestion)
	}
}

func TestInstructionsResolve_InfraError(t *testing.T) {
	root := instructionsResolveRoot(t, "", "")
	// A folder at the config file path cannot be read as a file.
	if err := os.MkdirAll(filepath.Join(root, ".sdlc-v2", "config.toml"), 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := instructionsResolve(root, InstructionsResolveIn{Key: "pr"})
	var ie *mcpserver.InfraError
	if !errors.As(err, &ie) {
		t.Fatalf("error is %T (%v), want *mcpserver.InfraError", err, err)
	}
	if !strings.Contains(ie.Msg, ".sdlc-v2/config.toml") {
		t.Errorf("Msg = %q, want it to name the file", ie.Msg)
	}
	if ie.Suggestion != "Make the file readable, then run the skill again." {
		t.Errorf("Suggestion = %q", ie.Suggestion)
	}
}

func TestInstructionsResolve_Description(t *testing.T) {
	for _, want := range []string{
		".sdlc-v2/config.toml", ".sdlc-v2/local.toml", "user-level config file",
		"The team list comes first.", "An empty project local list hides the user-level list.",
		"execute, commit, review, received-review, pr, harden or verify-pipeline",
		"DomainError", "DataError", "InfraError", "Writes nothing.",
	} {
		if !strings.Contains(instructionsResolveDescription, want) {
			t.Errorf("description does not contain %q", want)
		}
	}
}
