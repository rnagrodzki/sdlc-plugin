package tools

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rnagrodzki/sdlc-plugin/internal/config"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
	"github.com/rnagrodzki/sdlc-plugin/internal/state"
)

// registeredToolDescription returns the description of the tool name, as a
// client lists it over an in-memory MCP session.
func registeredToolDescription(t *testing.T, register func(*mcpserver.Server), name string) string {
	t.Helper()
	s := mcpserver.New("test", "0.0.0-test")
	register(s)
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
	resp, err := c.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	for _, tool := range resp.Tools {
		if tool.Name == name {
			return tool.Description
		}
	}
	t.Fatalf("no %q tool registered", name)
	return ""
}

// outcomeLimitRe finds a stated outcome limit such as "max 1000 characters"
// or "at most 1000 characters".
var outcomeLimitRe = regexp.MustCompile(`(?i)(?:max|at most) (\d+) characters`)

// TestInstructionOutcomeLimitTextMatchesConstant proves that each place that
// states the outcome limit in text (the schema tags and the tool
// descriptions, which cannot be built from the constant) states
// MaxInstructionOutcome.
func TestInstructionOutcomeLimitTextMatchesConstant(t *testing.T) {
	tag := func(typ any, field string) string {
		f, ok := reflect.TypeOf(typ).FieldByName(field)
		if !ok {
			t.Fatalf("%T has no field %s", typ, field)
		}
		return f.Tag.Get("jsonschema_description")
	}
	sources := map[string]string{
		"ShipStateIn.Detail tag":                tag(ShipStateIn{}, "Detail"),
		"ExecuteStateIn.InstructionOutcome tag": tag(ExecuteStateIn{}, "InstructionOutcome"),
		"ship_state tool description":           registeredToolDescription(t, RegisterShipStateTools, "ship_state"),
		"execute_state tool description":        registeredToolDescription(t, RegisterExecuteStateTools, "execute_state"),
	}
	for name, text := range sources {
		matches := outcomeLimitRe.FindAllStringSubmatch(text, -1)
		if len(matches) == 0 {
			t.Errorf("%s states no outcome limit", name)
			continue
		}
		for _, m := range matches {
			if n, _ := strconv.Atoi(m[1]); n != MaxInstructionOutcome {
				t.Errorf("%s states %q, want %d characters (MaxInstructionOutcome)", name, m[0], MaxInstructionOutcome)
			}
		}
	}
}

// TestShipStateToolDescriptionListsEveryAction pins the registered ship_state
// description to the dispatcher case labels: each accepted action has its own
// "- <action>" line. The enum tag, the Action description and the unknown
// action hint have their own tests.
func TestShipStateToolDescriptionListsEveryAction(t *testing.T) {
	desc := registeredToolDescription(t, RegisterShipStateTools, "ship_state")
	for _, a := range shipStateDispatcherActions(t) {
		listed := strings.Contains(desc, "\n- "+a+":") || strings.Contains(desc, "\n- "+a+" ")
		if !listed {
			t.Errorf("tool description has no line for accepted action %q", a)
		}
	}
}

// TestShipStateCommitCheck_PushesWorkDirNotRoot proves commit-check pushes the
// work directory: the ship state is in root, the repo is in workDir, and the
// push seam gets workDir.
func TestShipStateCommitCheck_PushesWorkDirNotRoot(t *testing.T) {
	const branch = "feat/cc-push-workdir"
	workDir := t.TempDir()
	initGitFixture(t, workDir)
	gitCommit(t, workDir, "initial")
	checkoutBranch(t, workDir, branch)
	root := t.TempDir()
	path := shipStateInitFixture(t, root, branch)
	setStepStatus(t, path, "execute", "completed", map[string]any{"completedAt": "2026-01-01T00:00:00Z"})
	setStepStatus(t, path, "commit", "in_progress", map[string]any{"startedAt": "2026-01-01T00:00:00Z"})
	calls := stubPushBestEffort(t, true, "")

	out, err := shipState(root, workDir, ShipStateIn{
		Action: "commit-check",
		Detail: map[string]any{"branch": branch},
	}, fixedNow(time.Date(2026, 1, 1, 0, 5, 0, 0, time.UTC)))
	if err != nil {
		t.Fatalf("commit-check: %v", err)
	}
	if !out.(ShipCommitCheckOut).StepCompleted {
		t.Fatalf("out = %+v, want stepCompleted", out)
	}
	if len(*calls) != 1 || (*calls)[0] != workDir {
		t.Errorf("push calls = %v, want one call in workDir %s (root is %s)", *calls, workDir, root)
	}
}

// TestShipStateInstructionRecordAllNoOpenRecord proves instructionAll with no
// open record of the key closes nothing, writes nothing, and says so.
func TestShipStateInstructionRecordAllNoOpenRecord(t *testing.T) {
	const branch = "feat/instr-all-none"
	dir, path := instrFixture(t, branch, instrList("pr", "a"))
	data := readStateData(t, path)
	data["instructions"].([]any)[0].(map[string]any)["status"] = "done"
	writeStateData(t, path, data)
	// A failing write seam proves no write: the call would return its error.
	useShipStateWrite(t, func(*state.State) error { return errors.New("disk full") })

	out, err := instrCall(dir, branch, "instruction-record", map[string]any{
		"key": "pr", "instructionAll": true, "instructionStatus": "not run", "instructionOutcome": "user stopped",
	}, instrT0)
	if err != nil {
		t.Fatalf("instruction-record: %v, want success with no write", err)
	}
	rec := out.(InstructionRecordResultOut)
	if rec.Updated != 0 || len(rec.Closed) != 0 {
		t.Errorf("record out = %+v, want updated 0 and no closed record", rec)
	}
	if got, want := rec.Summary, "0 open instructions of pr closed."; got != want {
		t.Errorf("summary = %q, want %q", got, want)
	}
	if got, want := rec.Next, "No record of pr is open. Continue with the next step."; got != want {
		t.Errorf("next = %q, want %q", got, want)
	}
}

// closedInstructions returns seeded records of list, each closed as done.
func closedInstructions(list []config.Instruction) []any {
	data := map[string]any{}
	instrSeed(data, list)
	recs := data[instrStateKey].([]any)
	for _, r := range recs {
		r.(map[string]any)[instrFieldStatus] = instrDone
	}
	return recs
}

// TestInstructionsNextRegisteredRendersNone calls the registered ship_state and
// execute_state tools for instructions-next with no open record, and checks
// that the Markdown a caller reads shows the instruction as (none).
func TestInstructionsNextRegisteredRendersNone(t *testing.T) {
	t.Run("ship_state", func(t *testing.T) {
		const branch = "feat/instr-none-mcp"
		dir := shipStateReadFixture(t, branch, map[string]any{"instructions": closedInstructions(instrList("pr", "a"))})
		t.Chdir(dir)
		res, text := callRegisteredTool(t, RegisterShipStateTools, "ship_state", map[string]any{
			"action": "instructions-next", "detail": map[string]any{"branch": branch, "key": "pr"},
		})
		if res.IsError {
			t.Fatalf("instructions-next failed:\n%s", text)
		}
		if !strings.Contains(text, "- instruction: (none)") {
			t.Errorf("text has no %q line:\n%s", "- instruction: (none)", text)
		}
	})
	t.Run("execute_state", func(t *testing.T) {
		const branch = "feat/exec-instr-none-mcp"
		dir := t.TempDir()
		initGitFixture(t, dir)
		gitCommit(t, dir, "initial")
		checkoutBranch(t, dir, branch)
		writeFile(t, filepath.Join(dir, paths.DataDir, "config.toml"), "")
		createExecState(t, dir, branch, map[string]any{
			"branch":       branch,
			"instructions": closedInstructions(instrList("execute", "a")),
		})
		t.Chdir(dir)
		res, text := callRegisteredTool(t, RegisterExecuteStateTools, "execute_state", map[string]any{"action": "instructions-next", "branch": branch})
		if res.IsError {
			t.Fatalf("instructions-next failed:\n%s", text)
		}
		if !strings.Contains(text, "- instruction: (none)") {
			t.Errorf("text has no %q line:\n%s", "- instruction: (none)", text)
		}
	})
}

// TestShipStateReadResumeBriefingOpenEndList proves read carries a
// resumeBriefing for a run whose steps are all done but whose end list is
// still open, and none once the run is stamped completed.
func TestShipStateReadResumeBriefingOpenEndList(t *testing.T) {
	setup := func(t *testing.T, branch string, stamped bool) string {
		t.Helper()
		dir, path := instrFixture(t, branch, instrList("ship", "Post the release note."))
		data := readStateData(t, path)
		for _, s := range data["steps"].([]any) {
			step := s.(map[string]any)
			step["status"] = "completed"
			step["startedAt"] = "2026-01-01T00:00:00Z"
			step["completedAt"] = "2026-01-01T00:01:00Z"
		}
		if stamped {
			data["pipelineStatus"] = "completed"
			data["pipelineCompletedAt"] = "2026-01-01T00:02:00Z"
		}
		writeStateData(t, path, data)
		return dir
	}
	read := func(t *testing.T, dir, branch string) map[string]any {
		t.Helper()
		out, err := instrCall(dir, branch, "read", nil, instrT0)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		return out.(map[string]any)
	}

	t.Run("open end list", func(t *testing.T) {
		const branch = "feat/instr-read-open"
		m := read(t, setup(t, branch, false), branch)
		if _, ok := m["resumeBriefing"]; !ok {
			t.Error("resumeBriefing absent, want it for an open end list")
		}
	})
	t.Run("stamped run", func(t *testing.T) {
		const branch = "feat/instr-read-stamped"
		m := read(t, setup(t, branch, true), branch)
		if _, ok := m["resumeBriefing"]; ok {
			t.Error("resumeBriefing present, want none for a stamped run")
		}
	})
}
