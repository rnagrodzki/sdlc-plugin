package tools

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
)

func instrStrPtr(v string) *string { return &v }
func instrBoolPtr(v bool) *bool    { return &v }

// parityResult is the part of a ship_state or execute_state instruction call
// that TestInstructionActionsParity compares.
type parityResult struct {
	class    string // ok, DomainError, DataError, InfraError or other
	msg      string // error message; empty on ok
	instr    string // "key #n" or "(none)"; instructions-next only
	warnings []string
}

func parityOf(out any, err error) parityResult {
	var (
		domErr   *mcpserver.DomainError
		dataErr  *mcpserver.DataError
		infraErr *mcpserver.InfraError
	)
	switch {
	case err == nil:
	case errors.As(err, &domErr):
		return parityResult{class: "DomainError", msg: domErr.Msg}
	case errors.As(err, &dataErr):
		return parityResult{class: "DataError", msg: dataErr.Msg}
	case errors.As(err, &infraErr):
		return parityResult{class: "InfraError", msg: infraErr.Msg}
	default:
		return parityResult{class: "other", msg: err.Error()}
	}
	res := parityResult{class: "ok", instr: "(none)"}
	var rec *InstructionRecordOut
	switch o := out.(type) {
	case InstructionsNextOut:
		rec, res.warnings = o.Instruction, o.Warnings
	default:
		res.instr = ""
	}
	if rec != nil {
		res.instr = rec.Key + " #" + strconv.Itoa(rec.Number)
	}
	return res
}

// TestInstructionActionsParity runs each shared input state through
// ship_state and execute_state and asserts the error class and the message or
// warning text, or the difference that the contract states for that row.
func TestInstructionActionsParity(t *testing.T) {
	type call struct {
		ship func(dir, branch string) (any, error)
		exec func(root, branch string) (any, error)
	}
	next := call{
		ship: func(dir, branch string) (any, error) {
			return instrCall(dir, branch, "instructions-next", map[string]any{"key": "execute"}, instrT0)
		},
		exec: func(root, branch string) (any, error) {
			return execInstrCall(root, branch, ExecuteStateIn{Action: "instructions-next"}, instrT0)
		},
	}
	recordFailed := call{
		ship: func(dir, branch string) (any, error) {
			return instrCall(dir, branch, "instruction-record", map[string]any{
				"key": "execute", "instructionNumber": float64(1), "instructionStatus": "failed", "instructionOutcome": "again",
			}, instrT0)
		},
		exec: func(root, branch string) (any, error) {
			return execInstrCall(root, branch, execInstrRecordIn(1, "failed", "again"), instrT0)
		},
	}

	// Each prep changes the state file at path the same way for both tools.
	editData := func(edit func(data map[string]any)) func(t *testing.T, path string) {
		return func(t *testing.T, path string) {
			data := readStateData(t, path)
			edit(data)
			writeStateData(t, path, data)
		}
	}
	seedClosed := editData(func(data map[string]any) {
		instrSeed(data, instrList("execute", "x"))
		data["instructions"].([]any)[0].(map[string]any)["status"] = "done"
	})

	rows := []struct {
		name       string
		prep       func(t *testing.T, path string)
		call       call
		ship, exec parityResult
	}{
		{
			name: "no instructions key",
			prep: func(*testing.T, string) {},
			call: next,
			// Difference: ship reads an empty list, execute seeds from config.
			ship: parityResult{class: "ok", instr: "(none)"},
			exec: parityResult{class: "ok", instr: "execute #1"},
		},
		{
			name: "instructions key of the wrong type",
			prep: editData(func(data map[string]any) { data["instructions"] = map[string]any{"a": 1} }),
			call: next,
			ship: parityResult{class: "DataError", msg: instrDamagedMsg},
			exec: parityResult{class: "DataError", msg: instrDamagedMsg},
		},
		{
			name: "closed record",
			prep: seedClosed,
			call: recordFailed,
			ship: parityResult{class: "DomainError", msg: "instruction execute #1 is closed (done)"},
			exec: parityResult{class: "DomainError", msg: "instruction execute #1 is closed (done)"},
		},
		{
			name: "flags.auto not a boolean",
			prep: editData(func(data map[string]any) {
				instrSeed(data, instrList("execute", "x"))
				flags, _ := data["flags"].(map[string]any)
				if flags == nil {
					flags = map[string]any{}
				}
				flags["auto"] = "yes"
				data["flags"] = flags
			}),
			call: next,
			// Difference: only ship reads flags.auto.
			ship: parityResult{class: "ok", instr: "execute #1", warnings: []string{"flags.auto of the ship state is string yes, want a boolean. Run mode --auto is used. Start a new run to set flags.auto again."}},
			exec: parityResult{class: "ok", instr: "execute #1"},
		},
		{
			name: "missing state file",
			prep: func(t *testing.T, path string) {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			},
			call: next,
			// Each tool keeps its own read error.
			ship: parityResult{class: "DataError"},
			exec: parityResult{class: "DataError"},
		},
		{
			name: "state path is a folder",
			prep: func(t *testing.T, path string) {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(path, 0o755); err != nil {
					t.Fatal(err)
				}
			},
			call: next,
			ship: parityResult{class: "InfraError"},
			exec: parityResult{class: "InfraError"},
		},
		{
			name: "state path is a symbolic link",
			prep: func(t *testing.T, path string) {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(filepath.Dir(path), "missing.json"), path); err != nil {
					t.Fatal(err)
				}
			},
			call: next,
			ship: parityResult{class: "InfraError"},
			exec: parityResult{class: "InfraError"},
		},
	}

	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			const branch = "feat/instr-parity"
			dir, shipPath := deferFixture(t, branch)
			row.prep(t, shipPath)
			root, execPath := execInstrFixture(t, branch, "x")
			row.prep(t, execPath)

			ship := parityOf(row.call.ship(dir, branch))
			exec := parityOf(row.call.exec(root, branch))
			for _, side := range []struct {
				tool      string
				got, want parityResult
			}{{"ship_state", ship, row.ship}, {"execute_state", exec, row.exec}} {
				if side.got.class != side.want.class {
					t.Errorf("%s class = %s (%s), want %s", side.tool, side.got.class, side.got.msg, side.want.class)
				}
				if side.want.msg != "" && side.got.msg != side.want.msg {
					t.Errorf("%s msg = %q, want %q", side.tool, side.got.msg, side.want.msg)
				}
				if side.want.instr != "" && side.got.instr != side.want.instr {
					t.Errorf("%s instruction = %q, want %q", side.tool, side.got.instr, side.want.instr)
				}
				if len(side.got.warnings) != len(side.want.warnings) || (len(side.want.warnings) > 0 && side.got.warnings[0] != side.want.warnings[0]) {
					t.Errorf("%s warnings = %q, want %q", side.tool, side.got.warnings, side.want.warnings)
				}
			}
		})
	}

	// A record input sent with another action: both tools refuse each of the
	// 4 fields. Difference: ship names detail.<field>, execute names <field>.
	execSet := map[string]func(in *ExecuteStateIn){
		"instructionNumber":  func(in *ExecuteStateIn) { in.InstructionNumber = intPtr(1) },
		"instructionAll":     func(in *ExecuteStateIn) { in.InstructionAll = instrBoolPtr(true) },
		"instructionStatus":  func(in *ExecuteStateIn) { in.InstructionStatus = instrStrPtr("done") },
		"instructionOutcome": func(in *ExecuteStateIn) { in.InstructionOutcome = instrStrPtr("ok") },
	}
	shipValues := map[string]any{
		"instructionNumber": float64(1), "instructionAll": true, "instructionStatus": "done", "instructionOutcome": "ok",
	}
	for _, field := range instrInputFields {
		t.Run("record input with another action/"+field, func(t *testing.T) {
			const branch = "feat/instr-parity-input"
			dir, _ := deferFixture(t, branch)
			root, _ := execInstrFixture(t, branch, "x")

			ship := parityOf(instrCall(dir, branch, "instructions-next", map[string]any{"key": "execute", field: shipValues[field]}, instrT0))
			in := ExecuteStateIn{Action: "instructions-next"}
			execSet[field](&in)
			exec := parityOf(execInstrCall(root, branch, in, instrT0))

			if ship.class != "DomainError" || ship.msg != "detail."+field+" is valid only with instruction-record" {
				t.Errorf("ship_state = %+v, want DomainError detail.%s is valid only with instruction-record", ship, field)
			}
			if exec.class != "DomainError" || exec.msg != field+" is valid only with instruction-record" {
				t.Errorf("execute_state = %+v, want DomainError %s is valid only with instruction-record", exec, field)
			}
		})
	}
}
