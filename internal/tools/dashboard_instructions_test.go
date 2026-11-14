package tools

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// dashInstrRec returns one instruction record map as a state file holds it.
func dashInstrRec(key string, number int, status, outcome string) map[string]any {
	rec := map[string]any{
		"key": key, "number": number, "source": "team",
		"text": "Do " + key + " " + string(rune('0'+number)), "status": status,
	}
	if outcome != "" {
		rec["outcome"] = outcome
	}
	return rec
}

func dashInstrRow(key string, number int, status, outcome string) DashboardInstruction {
	return DashboardInstruction{
		Key: key, Number: number, Source: "team",
		Text: "Do " + key + " " + string(rune('0'+number)), Status: status, Outcome: outcome,
	}
}

// dashInstrShipData returns the join ship state with a received-review and a
// pr step added, and the given records.
func dashInstrShipData(records ...any) map[string]any {
	data := dashJoinShipData()
	steps := data["steps"].([]any)
	data["steps"] = append(steps,
		map[string]any{"name": "received-review", "status": StepPending},
		map[string]any{"name": "pr", "status": StepPending},
	)
	if records != nil {
		data["instructions"] = records
	}
	return data
}

func dashInstrStepRows(p DashboardPipeline) map[string][]DashboardInstruction {
	out := map[string][]DashboardInstruction{}
	for _, s := range p.Steps {
		if len(s.Instructions) > 0 {
			out[s.Name] = s.Instructions
		}
	}
	return out
}

// TestDashboardInstructionMapping has one case for each row of the mapping
// table of the instruction records.
func TestDashboardInstructionMapping(t *testing.T) {
	tests := []struct {
		name      string
		file      string
		data      map[string]any
		extraExec bool // write a nested execute state that holds a record
		pickKind  string
		wantSteps map[string][]DashboardInstruction
		wantEnd   []DashboardInstruction
		wantLabel string
	}{
		{
			name:     "ship step with a step entry",
			file:     dashJoinShipFile,
			data:     dashInstrShipData(dashInstrRec("pr", 1, "failed", "Jira transition not found"), dashInstrRec("commit", 1, "done", "")),
			pickKind: "ship",
			wantSteps: map[string][]DashboardInstruction{
				"pr":     {dashInstrRow("pr", 1, "failed", "Jira transition not found")},
				"commit": {dashInstrRow("commit", 1, "done", "")},
			},
		},
		{
			name:     "commit-fixes goes to received-review",
			file:     dashJoinShipFile,
			data:     dashInstrShipData(dashInstrRec("commit-fixes", 1, "running", "")),
			pickKind: "ship",
			wantSteps: map[string][]DashboardInstruction{
				"received-review": {dashInstrRow("commit-fixes", 1, "running", "")},
			},
		},
		{
			name:      "ship key goes to the end list",
			file:      dashJoinShipFile,
			data:      dashInstrShipData(dashInstrRec("ship", 1, "pending", ""), dashInstrRec("ship", 2, "not run", "stopped")),
			pickKind:  "ship",
			wantEnd:   []DashboardInstruction{dashInstrRow("ship", 1, "pending", ""), dashInstrRow("ship", 2, "not run", "stopped")},
			wantLabel: "end of run (ship)",
		},
		{
			name:      "standalone execute key goes to the end list",
			file:      dashJoinExecFile("20261007T080100Z"),
			data:      withInstr(dashJoinExecData("feat/x", "2026-10-07T08:01:00Z", "a1b2c3d"), dashInstrRec("execute", 1, "done", "")),
			pickKind:  "execute",
			wantEnd:   []DashboardInstruction{dashInstrRow("execute", 1, "done", "")},
			wantLabel: "end of run (execute)",
		},
		{
			name:      "nested execute shows no row",
			file:      dashJoinShipFile,
			data:      dashInstrShipData(),
			extraExec: true,
			pickKind:  "ship",
		},
		{
			name: "key with no step entry goes after the ship records",
			file: dashJoinShipFile,
			// The orphan is first in the list, the ship record after it.
			data:      dashInstrShipData(dashInstrRec("await-remote-review", 1, "pending", ""), dashInstrRec("ship", 1, "pending", "")),
			pickKind:  "ship",
			wantEnd:   []DashboardInstruction{dashInstrRow("ship", 1, "pending", ""), dashInstrRow("await-remote-review", 1, "pending", "")},
			wantLabel: "end of run (ship)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := dashRoot(t)
			dashWriteState(t, root, tt.file, tt.data, dashJoinFresh)
			if tt.extraExec {
				exec := withInstr(dashJoinExecData("feat/x", "2026-10-07T08:01:00Z", "a1b2c3d"), dashInstrRec("execute", 1, "done", ""))
				dashWriteState(t, root, dashJoinExecFile("20261007T080100Z"), exec, dashJoinFresh)
			}
			repo := dashCollect(t, root)
			if len(repo.Pipelines) != 1 || repo.Pipelines[0].Kind != tt.pickKind {
				t.Fatalf("pipelines = %v, want one %s", dashJoinKinds(repo.Pipelines), tt.pickKind)
			}
			p := repo.Pipelines[0]
			if got := dashInstrStepRows(p); !reflect.DeepEqual(got, orEmptyRows(tt.wantSteps)) {
				t.Errorf("step rows = %+v, want %+v", got, tt.wantSteps)
			}
			if !reflect.DeepEqual(p.EndInstructions, tt.wantEnd) {
				t.Errorf("endInstructions = %+v, want %+v", p.EndInstructions, tt.wantEnd)
			}
			if p.EndLabel != tt.wantLabel {
				t.Errorf("endLabel = %q, want %q", p.EndLabel, tt.wantLabel)
			}
		})
	}
}

func withInstr(data map[string]any, records ...any) map[string]any {
	data["instructions"] = records
	return data
}

func orEmptyRows(m map[string][]DashboardInstruction) map[string][]DashboardInstruction {
	if m == nil {
		return map[string][]DashboardInstruction{}
	}
	return m
}

// TestDashboardInstructionAbsent checks that a run with no record, a null key,
// an empty list, or a damaged key has neither field in its JSON, and that a
// damaged key or a skipped record gives one issue with Ref "instructions".
func TestDashboardInstructionAbsent(t *testing.T) {
	cases := []struct {
		name      string
		set       bool // false leaves the key out of the state
		value     any
		wantIssue string // "" means no instruction issue
	}{
		{name: "no key"},
		{name: "null", set: true, value: nil},
		{name: "empty list", set: true, value: []any{}},
		{name: "not a list", set: true, value: "oops", wantIssue: "the page shows no instruction of this run"},
		{name: "list of text", set: true, value: []any{"a", "b"}, wantIssue: "the page shows no instruction of this run"},
		{name: "object", set: true, value: map[string]any{"key": "pr"}, wantIssue: "the page shows no instruction of this run"},
		{name: "only bad rows", set: true, value: []any{map[string]any{"key": "pr", "status": "weird"}, map[string]any{"status": "done"}},
			wantIssue: "the page shows no instruction of this run"},
		{name: "status outside the record statuses", set: true, value: []any{dashInstrRec("pr", 1, "weird", "")},
			wantIssue: "record 1 field status"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			data := dashInstrShipData()
			if tt.set {
				data["instructions"] = tt.value
			}
			root := dashRoot(t)
			dashWriteState(t, root, dashJoinShipFile, data, dashJoinFresh)
			p := dashOne(t, root)
			b, err := json.Marshal(p)
			if err != nil {
				t.Fatal(err)
			}
			// The colon matches a JSON key only: an issue Ref value
			// "instructions" is not a field.
			for _, field := range []string{`"instructions":`, `"endInstructions":`, `"endLabel":`} {
				if strings.Contains(string(b), field) {
					t.Errorf("pipeline JSON has %s: %s", field, b)
				}
			}
			var issues []DashboardIssue
			for _, is := range p.Issues {
				if is.Ref == "instructions" {
					issues = append(issues, is)
				}
			}
			if tt.wantIssue == "" {
				if len(issues) != 0 {
					t.Errorf("instruction issues = %+v, want none", issues)
				}
				return
			}
			if len(issues) != 1 || !strings.Contains(issues[0].Text, tt.wantIssue) || issues[0].Source != "state" {
				t.Errorf("instruction issues = %+v, want one state issue with %q", issues, tt.wantIssue)
			}
		})
	}
}

// TestDashboardInstructionStatusChange checks that a status change in the
// state file shows in the next collected snapshot, and that the slim page
// snapshot of a finished run keeps the rows.
func TestDashboardInstructionStatusChange(t *testing.T) {
	root := dashRoot(t)
	dashWriteState(t, root, dashJoinShipFile, dashInstrShipData(dashInstrRec("pr", 1, "running", "")), dashJoinFresh)
	if got := dashJoinStep(t, dashOne(t, root), "pr").Instructions; len(got) != 1 || got[0].Status != "running" {
		t.Fatalf("first snapshot rows = %+v, want one running row", got)
	}

	data := dashInstrShipData(dashInstrRec("pr", 1, "done", "moved"))
	data["pipelineStatus"] = "completed"
	data["pipelineCompletedAt"] = "2026-10-07T09:00:00Z"
	dashWriteState(t, root, dashJoinShipFile, data, dashJoinFresh)
	p := dashOne(t, root)
	got := dashJoinStep(t, p, "pr").Instructions
	if len(got) != 1 || got[0].Status != "done" || got[0].Outcome != "moved" {
		t.Fatalf("second snapshot rows = %+v, want one done row with the outcome", got)
	}
	slim := slimDashboardPipeline(p)
	if !slim.DetailDeferred {
		t.Fatal("completed run is not slim")
	}
	if got := dashJoinStep(t, slim, "pr").Instructions; len(got) != 1 {
		t.Errorf("slim rows = %+v, want the rows kept", got)
	}
}
