// ship_instructions.go holds the ship_state actions instructions-next and
// instruction-record, the record sweeps of fail, skip and cleanup-pipeline,
// and the in-flight check of the records. The record helpers and the parts
// that execute_state shares live in instruction_records.go.

package tools

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/config"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
)

// Outcomes of the record sweeps of fail, skip and cleanup-pipeline.
const (
	shipInstrOutcomeFailed    = "step failed or stopped"
	shipInstrOutcomeSkipped   = "step skipped"
	shipInstrOutcomeDidNotRun = "step did not run"
)

// Run modes that instructions-next returns.
const (
	shipRunModeAuto        = "--auto"
	shipRunModeInteractive = "interactive"
)

// shipInstrMatchAll accepts each record: the run-wide sweeps of fail and
// cleanup-pipeline close every open record.
func shipInstrMatchAll(map[string]any) bool { return true }

// shipRefuseInstructionInputs returns a DomainError for the first record input
// in detail. The dispatcher calls it for every action except
// instruction-record.
func shipRefuseInstructionInputs(detail map[string]any) error {
	return instrRefuseInputs(func(field string) bool {
		_, ok := detail[field]
		return ok
	}, "detail.")
}

// shipInstrKey reads detail.key and checks it against config.InstructionKeys.
func shipInstrKey(detail map[string]any) (string, error) {
	raw, ok := detail["key"]
	key, isStr := raw.(string)
	if !isStr && ok && raw != nil {
		key = fmt.Sprint(raw)
	}
	if !isStr || !slices.Contains(config.InstructionKeys, key) {
		return "", &mcpserver.DomainError{
			Msg:        fmt.Sprintf("key %q is not an instruction key", key),
			Suggestion: "Send detail.key as one of the instruction keys: " + strings.Join(config.InstructionKeys, ", ") + ".",
		}
	}
	return key, nil
}

// shipInstrNumber reads detail.instructionNumber. has is false when the key
// is absent or null. A value that is not a whole number is a DomainError.
func shipInstrNumber(detail map[string]any) (number int, has bool, err error) {
	raw, ok := detail["instructionNumber"]
	if !ok || raw == nil {
		return 0, false, nil
	}
	switch n := raw.(type) {
	case int:
		return n, true, nil
	case float64:
		if n == float64(int(n)) {
			return int(n), true, nil
		}
	}
	return 0, false, &mcpserver.DomainError{
		Msg:        fmt.Sprintf("instruction-record: detail.instructionNumber must be a whole number, got %v", raw),
		Suggestion: "Send the number from instructions-next as a JSON number, for example 2.",
	}
}

// shipInstrRecordInput reads and checks the instruction-record inputs of
// detail. It reads no state.
func shipInstrRecordInput(detail map[string]any) (instrRecordInput, error) {
	var in instrRecordInput
	var err error
	if in.key, err = shipInstrKey(detail); err != nil {
		return in, err
	}
	if in.number, in.hasNumber, err = shipInstrNumber(detail); err != nil {
		return in, err
	}
	if in.all, err = shipDetailBool(detail, "instruction-record", "instructionAll",
		`Send instructionAll:true with instructionStatus "not run" to close each open record of the key.`); err != nil {
		return in, err
	}
	if in.status, err = shipDetailString(detail, "instruction-record", "instructionStatus",
		"Use done, failed or not run."); err != nil {
		return in, err
	}
	if in.outcome, err = shipDetailString(detail, "instruction-record", "instructionOutcome",
		fmt.Sprintf("Send a short outcome of at most %d characters.", MaxInstructionOutcome)); err != nil {
		return in, err
	}
	return in, in.check()
}

// shipRunMode returns the run mode from flags.auto of the ship state, which
// ship_prepare sets. Only an explicit false is interactive. A value that is
// not a boolean is --auto with a warning.
func shipRunMode(data map[string]any) (string, []string) {
	flags, _ := data["flags"].(map[string]any)
	raw, ok := flags["auto"]
	if !ok || raw == nil {
		return shipRunModeAuto, nil
	}
	auto, isBool := raw.(bool)
	if !isBool {
		return shipRunModeAuto, []string{fmt.Sprintf(
			"flags.auto of the ship state is %T %v, want a boolean. Run mode %s is used. Start a new run to set flags.auto again.",
			raw, raw, shipRunModeAuto)}
	}
	if auto {
		return shipRunModeAuto, nil
	}
	return shipRunModeInteractive, nil
}

// shipInstrWriteError is the InfraError of a failed record write. The record
// keeps its stored status: a running record is closed by the next
// instructions-next call.
func shipInstrWriteError(err error) error {
	return &mcpserver.InfraError{
		Msg:        "cannot write the ship state: " + err.Error(),
		Suggestion: "Continue the run. The next instructions-next call closes the record. If writes keep failing, check that " + paths.DataDir + "/" + paths.RunsSubdir + "/ is writable.",
		Cause:      err,
	}
}

// shipStateInstructionsNext fails each running record of the key, starts the
// first pending one and returns it. The state is written only when a record
// changed. A failed write returns an InfraError and the stored records stay as
// they were.
func shipStateInstructionsNext(root, workDir string, in ShipStateIn, now func() time.Time) (any, error) {
	key, err := shipInstrKey(in.Detail)
	if err != nil {
		return nil, err
	}
	st, err := shipResolveAndFind(detailStr(in.Detail, "branch"), workDir, root)
	if err != nil {
		return nil, err
	}
	store := instrStore{data: st.Data, write: func() error { return shipStateWriteFunc(st) }, writeError: shipInstrWriteError}
	out, err := instrNextAction(store, key, now(), false,
		fmt.Sprintf("No open instruction for %s. Continue with the next step.", key))
	if err != nil {
		return nil, err
	}
	out.RunMode, out.Warnings = shipRunMode(st.Data)
	return out, nil
}

// shipStateInstructionRecord closes one running record of the key, or with
// instructionAll:true closes each open record of the key: a pending record
// becomes not run with the outcome, a running record becomes failed with the
// outcome "result not recorded, not run again". The input is checked before
// the state is read. A failed write returns an InfraError and the record
// stays running in the stored state.
func shipStateInstructionRecord(root, workDir string, in ShipStateIn, now func() time.Time) (any, error) {
	input, err := shipInstrRecordInput(in.Detail)
	if err != nil {
		return nil, err
	}
	st, err := shipResolveAndFind(detailStr(in.Detail, "branch"), workDir, root)
	if err != nil {
		return nil, err
	}
	store := instrStore{data: st.Data, write: func() error { return shipStateWriteFunc(st) }, writeError: shipInstrWriteError}
	return instrRecordAction(store, input, now(),
		fmt.Sprintf("Call instructions-next for %s.", input.key),
		fmt.Sprintf("No record of %s is open. Continue with the next step.", input.key))
}

// shipHasOpenInstruction reports whether a record of the run is pending or
// running. A damaged list counts as open: the run stays in flight, and the
// resume calls instructions-next, which returns the DataError that names the
// damage. The reportData instruction summary of read also names it.
func shipHasOpenInstruction(data map[string]any) bool {
	records, err := instrRecords(data)
	if err != nil {
		return true
	}
	for _, r := range records {
		if status := instrString(r, instrFieldStatus); status == instrPending || status == instrRunning {
			return true
		}
	}
	return false
}
