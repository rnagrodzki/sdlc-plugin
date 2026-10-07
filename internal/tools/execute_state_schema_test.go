package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// TestExecuteStateSchema_AcceptsWrittenState drives the real execute_state
// handlers through a full run (init, wave-start, wave-progress, task-done,
// task-fail, wave-done, wave-committed, wave-fail, wave-split, issue-draft,
// decide, drift-log, cleanup) and validates the state file they leave on
// disk against plugins/sdlc/schemas/execute-state.schema.json. The schema
// sets additionalProperties:false at the top level, so any key the code
// writes but the schema does not declare fails here.
func TestExecuteStateSchema_AcceptsWrittenState(t *testing.T) {
	schemaPath, err := filepath.Abs(filepath.Join("..", "..", "plugins", "sdlc", "schemas", "execute-state.schema.json"))
	if err != nil {
		t.Fatalf("abs schema path: %v", err)
	}
	sch, err := jsonschema.NewCompiler().Compile(schemaPath)
	if err != nil {
		t.Fatalf("compile schema: %v", err)
	}

	root := t.TempDir()
	seedInitConfig(t, root)
	clock := fixedClock(testNow)
	const branch = "feat/schema"

	call := func(in ExecuteStateIn) any {
		t.Helper()
		in.Branch = branch
		out, err := executeState(root, root, in, clock)
		if err != nil {
			t.Fatalf("%s: %v", in.Action, err)
		}
		return out
	}

	// init with no sessionId, no totalTasks and no plan: the code writes
	// sessionId:null, totalTasks:0, planPath:null, planHash:null.
	initOut := call(ExecuteStateIn{
		Action:         "init",
		Quality:        "balanced",
		PlannedTaskIds: []string{"T1", "T2", "T3"},
		CommitWaves:    "false",
	}).(map[string]any)
	filePath := initOut["filePath"].(string)

	startOut := call(ExecuteStateIn{
		Action:    "wave-start",
		Wave:      intPtr(1),
		TasksJSON: `[{"id":"T1","name":"one","description":"d1","files":["a.go"]},{"id":"T2","name":"two","description":"d2","files":["b.go"]}]`,
	}).(ExecWaveNarrationOut)
	runID := startOut.RunID

	// A progress claim for T2, so task-fail harvests a resumeFrom onto its row.
	call(ExecuteStateIn{
		Action:         "wave-progress",
		RunID:          runID,
		TaskID:         "T2",
		Phase:          "editing",
		AcceptanceDone: []int{0},
		FilesTouched:   []string{"b.go"},
		Blocker:        "waiting",
	})

	call(ExecuteStateIn{
		Action:       "task-done",
		Wave:         intPtr(1),
		TaskID:       "T1",
		TaskName:     "one",
		Complexity:   "Standard",
		Risk:         "Low",
		FilesChanged: `["a.go"]`,
		VerifyToken:  `["go test ./... ok"]`,
	})
	call(ExecuteStateIn{
		Action:     "task-fail",
		Wave:       intPtr(1),
		TaskID:     "T2",
		TaskName:   "two",
		Complexity: "Standard",
		Risk:       "Low",
		RunID:      runID,
		ErrorText:  "boom",
	})
	call(ExecuteStateIn{Action: "wave-done", Wave: intPtr(1), Decisions: `["kept it simple"]`, TimedOut: true})
	// No sha: the code records committedSha:null on the wave.
	call(ExecuteStateIn{Action: "wave-committed", Wave: intPtr(1)})

	call(ExecuteStateIn{Action: "wave-fail", Wave: intPtr(2), ErrorText: "wave failed"})
	call(ExecuteStateIn{Action: "wave-split", Wave: intPtr(3), Dispatched: `["T3","T4"]`, MissingIds: `["T4"]`})
	call(ExecuteStateIn{Action: "issue-draft", TaskID: "T2", IssueDraftTitle: "follow up", IssueDraftBody: "body", IssueDraftLabels: []string{"bug"}})
	call(ExecuteStateIn{Action: "decide", DecideType: "guardrail", DecideID: "no-secrets", DecideDecision: "override", DecideReason: "test"})
	call(ExecuteStateIn{Action: "drift-log", DriftSeverity: "warning", DriftSummary: "drift", DriftDetail: "detail", Wave: intPtr(1), TaskID: "T1"})
	call(ExecuteStateIn{Action: "cleanup"})

	raw, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("read state file: %v", err)
	}
	// Guard that the run really produced the keys this test exists for.
	for _, key := range []string{`"sessionId"`, `"commitWaves"`, `"committedSha"`, `"splitTree"`, `"verifyTokens"`, `"attempt"`, `"resumeFrom"`, `"runStatus"`} {
		if !strings.Contains(string(raw), key) {
			t.Errorf("state file has no %s key; the run did not exercise it", key)
		}
	}

	inst, err := jsonschema.UnmarshalJSON(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatalf("unmarshal state: %v", err)
	}
	if err := sch.Validate(inst); err != nil {
		t.Errorf("state file does not match execute-state.schema.json: %v\nstate: %s", err, raw)
	}

	// Wave objects and task rows allow extra keys, so Validate alone would
	// not notice a key the schema forgot to declare. Check every key the
	// run wrote on them is a declared property.
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("decode state: %v", err)
	}
	var schemaDoc map[string]any
	schemaRaw, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	if err := json.Unmarshal(schemaRaw, &schemaDoc); err != nil {
		t.Fatalf("decode schema: %v", err)
	}
	waveItems := schemaDoc["properties"].(map[string]any)["waves"].(map[string]any)["items"].(map[string]any)
	waveProps := waveItems["properties"].(map[string]any)
	taskProps := waveProps["tasks"].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)
	for _, w := range doc["waves"].([]any) {
		wm := w.(map[string]any)
		for k := range wm {
			if _, ok := waveProps[k]; !ok {
				t.Errorf("wave %v has key %q that the schema does not declare", wm["number"], k)
			}
		}
		for _, row := range wm["tasks"].([]any) {
			for k := range row.(map[string]any) {
				if _, ok := taskProps[k]; !ok {
					t.Errorf("task row in wave %v has key %q that the schema does not declare", wm["number"], k)
				}
			}
		}
	}
}

// TestExecuteStateSchema_AcceptsPlannedTasks covers the optional plannedTasks
// key. TestExecuteStateSchema_AcceptsWrittenState runs init with no plan, so
// it proves the schema accepts state without the key. This test runs init
// with a plan file and proves the schema accepts the key init then writes.
func TestExecuteStateSchema_AcceptsPlannedTasks(t *testing.T) {
	root := t.TempDir()
	seedInitConfig(t, root)
	planPath := filepath.Join(root, "plan.md")
	writeFile(t, planPath, "# Plan\n\n### Task 1: First task\n\nBody.\n\n### Task 2: Second task\n")

	out, err := executeState(root, root, ExecuteStateIn{
		Action:         "init",
		Branch:         "feat/schema-plan",
		Quality:        "balanced",
		PlanPath:       planPath,
		PlanHash:       "abc123",
		PlannedTaskIds: []string{"1", "2"},
	}, fixedClock(testNow))
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	filePath := out.(map[string]any)["filePath"].(string)

	raw, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("read state file: %v", err)
	}
	// Guard that init really wrote the key this test exists for.
	if !strings.Contains(string(raw), `"plannedTasks"`) {
		t.Fatalf("state file has no plannedTasks key; the run did not exercise it\nstate: %s", raw)
	}
	assertStateMatchesSchema(t, filePath)
}
