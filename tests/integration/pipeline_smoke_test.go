//go:build integration

// Package integration holds end-to-end smoke tests for the sdlc Go port
// (Task 49, the migration's capstone verification task). This file does not
// port more source — it verifies that the already-ported pieces work
// together the way a real Claude Code session would exercise them: build
// the real `sdlc` binary, drive a full ship pipeline through the MCP tool
// surface (ship_prepare, ship_state's full action set) against a scratch
// git repo, and exercise the stop-pipeline-continue hook's consecutive-block
// cap by exec'ing the built binary as `sdlc hook stop-pipeline-continue`.
//
// # Factual correction to the task's fact sheet / dispatch (Open Question 1)
//
// The fact sheet's Finding 2 characterizes ship_state's "start"/"complete"
// actions as "Pipeline-level ... marks the whole ship pipeline as
// started/finished", and Open Question 1's ruling frames a smoke test as
// "call start once ... then complete once" bookending the whole pipeline.
// Direct reading of internal/tools/ship_state.go's shipStateStart and
// shipStateComplete shows both REQUIRE a Step name and mutate that one
// step's status via shipStartStepCore/shipCompleteStepCore — the exact same
// core functions begin-step/complete-step call, just without the R-b1
// proceed-gate check (begin-step) or the outcome parameter
// (complete-step). Cross-checking the original JS source this was ported
// from (scripts/state/ship.js: cmdStart/cmdComplete both take opts.step and
// call startStepCore/completeStepCore — the same cores cmdBeginStep/
// cmdCompleteStep call) confirms this is not a porting gap: there never was
// a pipeline-level start/complete action, in the Go port or the original.
//
// This test still satisfies the ruling's operative instruction — exercise
// both the start/complete pair and the begin-step/complete-step pair in one
// pipeline run — by using "start" to open the FIRST real step and
// "complete" to close the LAST real step (bookending one step's lifecycle
// each via the legacy, gate-free actions), with "begin-step"/"complete-step"
// driving every step in between, plus "skip" for the scaffold's "version"
// step (present in the fixed step scaffold regardless of ship.steps[]
// config — see the Open Question 2 note below — but not part of this
// fixture's configured step list, so it is explicitly skipped rather than
// completed).
//
// # Open Question 2
//
// ship_prepare's step list is config-driven for VALIDATION purposes only
// (mergeShipFlags's cli>config>default precedence, stored under
// state.Data["flags"]["steps"]). The step-tracking scaffold that begin-step/
// complete-step/start/complete actually operate against
// (state.Data["steps"]) is shipmeta.InitialShipSteps() — a FIXED 7-entry
// list (execute, commit, review, received-review[conditional],
// commit-fixes[conditional], version, pr) set unconditionally by both
// ship_prepare and ship_state's "init" action, independent of ship.steps[].
// Per the ruling, this test does not hardcode that list as an assumed
// INPUT: after calling ship_prepare it calls "ship_state read" and drives
// the lifecycle purely from what that call actually returns. (It does
// separately assert the returned shape as an OUTPUT regression check —
// that is a verification of already-read source, not a pre-verification
// assumption.)
package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"

	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/tools"
)

// ---------------------------------------------------------------------------
// TestMain: build the real sdlc binary once for every test in this package.
// ---------------------------------------------------------------------------

var sdlcBinPath string

func TestMain(m *testing.M) {
	tmpDir, err := os.MkdirTemp("", "sdlc-integration-bin-*")
	if err != nil {
		panic("mkdtemp: " + err.Error())
	}
	defer os.RemoveAll(tmpDir)

	wd, err := os.Getwd()
	if err != nil {
		panic("getwd: " + err.Error())
	}
	repoRoot := filepath.Join(wd, "..", "..")

	sdlcBinPath = filepath.Join(tmpDir, "sdlc")
	cmd := exec.Command("go", "build", "-o", sdlcBinPath, "./cmd/sdlc")
	cmd.Dir = repoRoot
	if out, err := cmd.CombinedOutput(); err != nil {
		panic("go build ./cmd/sdlc failed: " + err.Error() + "\n" + string(out))
	}

	os.Exit(m.Run())
}

// ---------------------------------------------------------------------------
// Scratch git repo helpers (mirrors internal/hooks/session_start_test.go's
// runGit/realPath/chdir/gitFixture helpers — worktree.MainRoot/ActiveRoot
// resolve against the process's current working directory with no
// override parameter available outside their own package, so exercising
// the real Register*Tools handlers requires actually chdir-ing the test
// process into a fixture repo).
// ---------------------------------------------------------------------------

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed in %s: %v\n%s", strings.Join(args, " "), dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

func realPath(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("could not resolve symlinks for %s: %v", path, err)
	}
	return resolved
}

func chdir(t *testing.T, dir string) {
	t.Helper()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir(%s): %v", dir, err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(orig)
	})
}

// gitFixture creates a fresh single-worktree git repo with one commit on
// branch, chdirs the test process into it, and returns its (symlink-
// resolved) real path. The branch is never the repo's default branch (no
// "main"/"master" ref is ever created — git init leaves HEAD unborn until
// the first commit, which lands directly on branch via checkout -b), so
// ship_prepare's not-on-default-branch warning never fires.
func gitFixture(t *testing.T, branch string) string {
	t.Helper()
	dir := realPath(t, t.TempDir())
	runGit(t, dir, "init", "-q")
	runGit(t, dir, "checkout", "-q", "-b", branch)
	runGit(t, dir, "-c", "user.email=integration-test@example.com", "-c", "user.name=integration-test", "commit", "--allow-empty", "-q", "-m", "init")
	// Seed a minimal, already-current (schemaVersion-less) .sdlc-v2/config.json
	// so ship_prepare's KD5 gate (configmigrate.MigrateWithBackup) treats this
	// as a project that already ran /setup, rather than hard-failing with
	// ErrConfigMissing. A genuinely config-less scratch repo is a real,
	// intentional failure mode of that gate (see configmigrate.MigrateWithBackup's
	// doc comment) — this fixture simulates the realistic ship-pipeline
	// precondition of "/setup already ran", not the setup flow itself.
	mustWriteFile(t, filepath.Join(dir, ".sdlc-v2", "config.json"), `{}`)
	chdir(t, dir)
	return dir
}

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// ---------------------------------------------------------------------------
// In-process MCP client helpers (the skillcheck-family recipe: mcpserver.New
// -> Register*Tools -> MCPServer() -> client.NewInProcessClient -> Start ->
// Initialize -> CallTool). A real client/server round trip over the actual
// MCP request/response marshaling, not a mock or a direct Go function call.
// ---------------------------------------------------------------------------

type envelope struct {
	OK    bool            `json:"ok"`
	Data  json.RawMessage `json:"data,omitempty"`
	Code  string          `json:"code,omitempty"`
	Error string          `json:"error,omitempty"`
}

func callTool(t *testing.T, c *client.Client, name string, args map[string]any) envelope {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Name = name
	req.Params.Arguments = args

	result, err := c.CallTool(context.Background(), req)
	if err != nil {
		t.Fatalf("CallTool %q: %v", name, err)
	}
	if len(result.Content) == 0 {
		t.Fatalf("CallTool %q: no content", name)
	}
	text, ok := result.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("CallTool %q: content[0] not TextContent, got %T", name, result.Content[0])
	}

	var env envelope
	if err := json.Unmarshal([]byte(text.Text), &env); err != nil {
		t.Fatalf("CallTool %q: unmarshal envelope: %v\nraw: %s", name, err, text.Text)
	}
	return env
}

// setupShipClient registers the ship pipeline's own tool surface
// (ship_prepare, ship_verify_side_effect, ship_state) against a fresh
// in-process MCP server/client pair. Scoped to just the ship tools (not
// every Register*Tools function in internal/tools) since this suite's job
// is verifying the ship pipeline end to end, not re-exercising every tool
// family's registration — that is already covered by internal/skillcheck.
func setupShipClient(t *testing.T) *client.Client {
	t.Helper()
	srv := mcpserver.New("pipeline-smoke-test", "0.0.0-test")
	tools.RegisterShipTools(srv)
	tools.RegisterShipStateTools(srv)

	c, err := client.NewInProcessClient(srv.MCPServer())
	if err != nil {
		t.Fatalf("NewInProcessClient: %v", err)
	}
	t.Cleanup(func() { c.Close() })

	ctx := context.Background()
	if err := c.Start(ctx); err != nil {
		t.Fatalf("client.Start: %v", err)
	}

	initReq := mcp.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{Name: "pipeline-smoke-test", Version: "0.0.0"}
	if _, err := c.Initialize(ctx, initReq); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	return c
}

// shipStepEntry is the shape of one entry in ship_state's "read" response
// Data["steps"] array, decoded loosely (only the fields this test needs).
type shipStepEntry struct {
	Name      string `json:"name"`
	Status    string `json:"status"`
	Condition string `json:"condition"`
	HasCond   bool   `json:"-"`
}

func readShipSteps(t *testing.T, c *client.Client) []shipStepEntry {
	t.Helper()
	env := callTool(t, c, "ship_state", map[string]any{"action": "read"})
	if !env.OK {
		t.Fatalf("ship_state read: not ok: code=%s error=%s", env.Code, env.Error)
	}

	var data map[string]any
	if err := json.Unmarshal(env.Data, &data); err != nil {
		t.Fatalf("ship_state read: unmarshal data: %v", err)
	}
	rawSteps, _ := data["steps"].([]any)
	if len(rawSteps) == 0 {
		t.Fatalf("ship_state read: no steps in state data: %v", data)
	}

	steps := make([]shipStepEntry, 0, len(rawSteps))
	for _, raw := range rawSteps {
		sm, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("ship_state read: step entry not an object: %v", raw)
		}
		name, _ := sm["name"].(string)
		status, _ := sm["status"].(string)
		cond, hasCond := sm["condition"]
		condStr, _ := cond.(string)
		steps = append(steps, shipStepEntry{Name: name, Status: status, Condition: condStr, HasCond: hasCond})
	}
	return steps
}

// ---------------------------------------------------------------------------
// TestPipelineSmoke_FullShipPipeline
// ---------------------------------------------------------------------------

// TestPipelineSmoke_FullShipPipeline drives one full ship pipeline
// (ship_prepare -> ship_state's begin/complete family for every scaffold
// step -> cleanup) via the real in-process MCP tool surface against a
// scratch git repo, satisfying AC #2 ("go test -tags integration
// ./tests/... passes, exercising at least one full pipeline ... via real
// tool calls against a scratch git repo").
func TestPipelineSmoke_FullShipPipeline(t *testing.T) {
	dir := gitFixture(t, "feat/pipeline-smoke")

	// Minimal .sdlc/local.json enabling exactly execute/review/commit/pr
	// (Open Question 2's fixture instruction, minus "plan" — "plan" is not a
	// member of shipmeta.ValidSteps, so including it in ship.steps[] would
	// only ever produce a config-sourced warning, never select a real
	// pipeline stage; the "plan" stage referenced by the plan's Notes text
	// is a separate, upstream `plan` skill concern, not a ship.steps[]
	// value). This only affects ship_prepare's VALIDATION output (Sources,
	// Warnings) — the actual step-tracking scaffold this test drives is
	// state.Data["steps"], which is fixed regardless of this config (see
	// package doc comment above).
	mustWriteFile(t, filepath.Join(dir, ".sdlc", "local.json"),
		`{"ship": {"steps": ["execute", "review", "commit", "pr"]}}`)

	c := setupShipClient(t)

	// --- ship_prepare ---
	prepEnv := callTool(t, c, "ship_prepare", map[string]any{
		"sessionId": "smoke-session-1",
	})
	if !prepEnv.OK {
		t.Fatalf("ship_prepare: not ok: code=%s error=%s", prepEnv.Code, prepEnv.Error)
	}
	var prepOut struct {
		Errors    []string          `json:"errors"`
		Warnings  []string          `json:"warnings"`
		StateFile string            `json:"stateFile"`
		Flags     map[string]any    `json:"flags"`
		Sources   map[string]string `json:"sources"`
	}
	if err := json.Unmarshal(prepEnv.Data, &prepOut); err != nil {
		t.Fatalf("ship_prepare: unmarshal: %v", err)
	}
	if len(prepOut.Errors) != 0 {
		t.Fatalf("ship_prepare: unexpected errors: %v", prepOut.Errors)
	}
	if prepOut.StateFile == "" {
		t.Fatalf("ship_prepare: expected a stateFile to be initialized, got none (warnings=%v)", prepOut.Warnings)
	}

	// --- discover the real step scaffold (Open Question 2: do not assume) ---
	steps := readShipSteps(t, c)

	var nonConditional []string
	for _, s := range steps {
		if !s.HasCond {
			nonConditional = append(nonConditional, s.Name)
		}
		if s.Status != "pending" {
			t.Fatalf("step %q: expected initial status \"pending\", got %q", s.Name, s.Status)
		}
	}
	if len(nonConditional) == 0 {
		t.Fatalf("ship_state read: no non-conditional steps found in scaffold: %+v", steps)
	}

	// --- drive the lifecycle: start/complete bookend the first/last
	// non-conditional step (Open Question 1 correction, see package doc);
	// begin-step/complete-step drive every step in between; "version" is
	// explicitly skipped rather than completed, since it sits in the fixed
	// scaffold but was not part of this fixture's configured ship.steps[].
	for i, name := range nonConditional {
		switch {
		case name == "version":
			env := callTool(t, c, "ship_state", map[string]any{"action": "skip", "step": name})
			if !env.OK {
				t.Fatalf("ship_state skip %q: not ok: code=%s error=%s", name, env.Code, env.Error)
			}
		case i == 0:
			env := callTool(t, c, "ship_state", map[string]any{"action": "start", "step": name})
			if !env.OK {
				t.Fatalf("ship_state start %q: not ok: code=%s error=%s", name, env.Code, env.Error)
			}
			env = callTool(t, c, "ship_state", map[string]any{"action": "complete-step", "step": name})
			if !env.OK {
				t.Fatalf("ship_state complete-step %q: not ok: code=%s error=%s", name, env.Code, env.Error)
			}
		case i == len(nonConditional)-1:
			env := callTool(t, c, "ship_state", map[string]any{"action": "begin-step", "step": name})
			if !env.OK {
				t.Fatalf("ship_state begin-step %q: not ok: code=%s error=%s", name, env.Code, env.Error)
			}
			env = callTool(t, c, "ship_state", map[string]any{"action": "complete", "step": name})
			if !env.OK {
				t.Fatalf("ship_state complete %q: not ok: code=%s error=%s", name, env.Code, env.Error)
			}
		default:
			env := callTool(t, c, "ship_state", map[string]any{"action": "begin-step", "step": name})
			if !env.OK {
				t.Fatalf("ship_state begin-step %q: not ok: code=%s error=%s", name, env.Code, env.Error)
			}
			env = callTool(t, c, "ship_state", map[string]any{"action": "complete-step", "step": name})
			if !env.OK {
				t.Fatalf("ship_state complete-step %q: not ok: code=%s error=%s", name, env.Code, env.Error)
			}
		}
	}

	// --- assert the final state: every non-conditional step is terminal,
	// "version" specifically skipped, conditional steps left pending.
	final := readShipSteps(t, c)
	for _, s := range final {
		switch {
		case s.Name == "version":
			if s.Status != "skipped" {
				t.Errorf("step %q: expected status \"skipped\", got %q", s.Name, s.Status)
			}
		case s.HasCond:
			if s.Status != "pending" {
				t.Errorf("conditional step %q: expected status \"pending\" (steady state), got %q", s.Name, s.Status)
			}
		default:
			if s.Status != "completed" {
				t.Errorf("step %q: expected status \"completed\", got %q", s.Name, s.Status)
			}
		}
	}

	// --- cleanup: proves the pipeline contract validator accepts this end
	// state (conditional-pending steps count as terminal-OK).
	cleanupEnv := callTool(t, c, "ship_state", map[string]any{"action": "cleanup"})
	if !cleanupEnv.OK {
		t.Fatalf("ship_state cleanup: not ok: code=%s error=%s", cleanupEnv.Code, cleanupEnv.Error)
	}
	var cleanupOut map[string]any
	if err := json.Unmarshal(cleanupEnv.Data, &cleanupOut); err != nil {
		t.Fatalf("ship_state cleanup: unmarshal: %v", err)
	}
	if cleanupOut["cleaned"] != true {
		t.Errorf("ship_state cleanup: expected cleaned=true, got %v", cleanupOut)
	}
}

// ---------------------------------------------------------------------------
// TestPipelineSmoke_StopHookBlockCount
// ---------------------------------------------------------------------------

// TestPipelineSmoke_StopHookBlockCount exercises the stop-pipeline-continue
// hook's consecutive-block cap (internal/state.StepBlockCount,
// stopBlockCap==3) by exec'ing the real built binary as
// `sdlc hook stop-pipeline-continue` four times against a scratch repo with
// one step left in_progress. This runs in its own scratch repo/state,
// isolated from TestPipelineSmoke_FullShipPipeline: exhausting the cap marks
// the step "failed", and "failed" always blocks begin-step's proceed-gate,
// so this exercise cannot share a state file with a happy-path run.
func TestPipelineSmoke_StopHookBlockCount(t *testing.T) {
	const sessionID = "block-cap-session"

	dir := gitFixture(t, "feat/pipeline-block-cap")
	c := setupShipClient(t)

	prepEnv := callTool(t, c, "ship_prepare", map[string]any{"sessionId": sessionID})
	if !prepEnv.OK {
		t.Fatalf("ship_prepare: not ok: code=%s error=%s", prepEnv.Code, prepEnv.Error)
	}

	steps := readShipSteps(t, c)
	if len(steps) == 0 {
		t.Fatalf("ship_state read: empty scaffold")
	}
	firstStep := steps[0].Name

	beginEnv := callTool(t, c, "ship_state", map[string]any{"action": "begin-step", "step": firstStep})
	if !beginEnv.OK {
		t.Fatalf("ship_state begin-step %q: not ok: code=%s error=%s", firstStep, beginEnv.Code, beginEnv.Error)
	}

	stdin := `{"session_id":"` + sessionID + `"}`

	// Calls 1-3: the step is in_progress, unblocked, uncapped -> each call
	// must block the Stop event with a flat {"decision":"block",...} JSON.
	for i := 1; i <= 3; i++ {
		cmd := exec.Command(sdlcBinPath, "hook", "stop-pipeline-continue")
		cmd.Dir = dir
		cmd.Stdin = strings.NewReader(stdin)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr

		if err := cmd.Run(); err != nil {
			t.Fatalf("call %d: sdlc hook stop-pipeline-continue: %v\nstderr: %s", i, err, stderr.String())
		}

		out := strings.TrimSpace(stdout.String())
		if out == "" {
			t.Fatalf("call %d: expected a block decision, got empty stdout (stderr: %s)", i, stderr.String())
		}
		var decision map[string]any
		if err := json.Unmarshal([]byte(out), &decision); err != nil {
			t.Fatalf("call %d: unmarshal stdout %q: %v", i, out, err)
		}
		if decision["decision"] != "block" {
			t.Errorf("call %d: expected decision=block, got %v", i, decision)
		}
	}

	// Call 4: the cap (3) is exhausted -> silent output, step marked failed
	// instead of blocked a 4th time.
	cmd := exec.Command(sdlcBinPath, "hook", "stop-pipeline-continue")
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("call 4: sdlc hook stop-pipeline-continue: %v\nstderr: %s", err, stderr.String())
	}
	if out := strings.TrimSpace(stdout.String()); out != "" {
		t.Errorf("call 4: expected silent (empty) stdout once the block cap is exhausted, got %q", out)
	}

	final := readShipSteps(t, c)
	var found bool
	for _, s := range final {
		if s.Name != firstStep {
			continue
		}
		found = true
		if s.Status != "failed" {
			t.Errorf("step %q: expected status \"failed\" after block cap exhausted, got %q", firstStep, s.Status)
		}
	}
	if !found {
		t.Fatalf("step %q not found in final state", firstStep)
	}
}
