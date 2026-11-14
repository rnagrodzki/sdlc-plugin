// instruction_records.go holds the helpers for the instruction records that
// ship state and execute state keep under the state key "instructions".
// Both tools share one record shape, so one helper set keeps them equal.
//
// A record moves one way only:
//
//	pending -> running -> done | failed | not run
//
// A closed record (done, failed, not run) never changes, so each instruction
// runs at most once. The helpers change a state map in memory and write
// nothing to disk. Every helper checks the shape of the list before it
// changes a record, so a damaged state is refused before any change.
//
// The second half of the file holds the parts of the instructions-next and
// instruction-record actions that ship_state and execute_state share: the
// input rules, the output types and the read-change-write flow. Each tool
// passes its key, its state write and its texts.

package tools

import (
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/rnagrodzki/sdlc-plugin/internal/config"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
)

// Instruction record statuses.
const (
	instrPending = "pending"
	instrRunning = "running"
	instrDone    = "done"
	instrFailed  = "failed"
	instrNotRun  = "not run"
)

// instrStatuses is the closed set of record statuses.
var instrStatuses = []string{instrPending, instrRunning, instrDone, instrFailed, instrNotRun}

// instrClosingStatuses are the statuses that instruction-record accepts.
var instrClosingStatuses = []string{instrDone, instrFailed, instrNotRun}

const (
	// MaxInstructionOutcome is the longest outcome, in characters.
	MaxInstructionOutcome = 1000
	// instrOutcomeUnrecorded is the outcome of a running record that a later
	// call closes without a recorded result.
	instrOutcomeUnrecorded = "result not recorded, not run again"
	// instrDecidedPrefix starts the outcome of an instruction whose question
	// the agent decided itself. The summary lists such a record.
	instrDecidedPrefix = "decided:"
)

// instrStateKey is the state key of the record list.
const instrStateKey = "instructions"

// Record field names.
const (
	instrFieldKey     = "key"
	instrFieldNumber  = "number"
	instrFieldSource  = "source"
	instrFieldText    = "text"
	instrFieldNormalQ = "normalQuestions"
	instrFieldCriticQ = "criticalQuestions"
	instrFieldOnNoQ   = "onQuestionNotAllowed"
	instrFieldStatus  = "status"
	instrFieldOutcome = "outcome"
	instrFieldStarted = "startedAt"
	instrFieldEnded   = "endedAt"
)

// Text of the DataError for a damaged record list.
const (
	instrDamagedMsg    = "run state key instructions is not a list of records"
	instrDamagedAdvice = "Start a new run. The run state is damaged."
)

// instrSummaryOut is the summary of the records of one run. Total counts every
// record. Lines holds one line for each failed, not run or decided record.
type instrSummaryOut struct {
	Total  int      `json:"total"`
	Done   int      `json:"done"`
	Failed int      `json:"failed"`
	NotRun int      `json:"notRun"`
	Lines  []string `json:"lines"` // [] never null
}

// instrValidStatus reports whether s is a record status.
func instrValidStatus(s string) bool { return slices.Contains(instrStatuses, s) }

// instrSeed writes one pending record for each instruction of list, replacing
// any earlier records in data. The question flags come from the config
// instruction only. The text of an instruction never changes them.
func instrSeed(data map[string]any, list []config.Instruction) {
	records := make([]any, 0, len(list))
	for _, in := range list {
		records = append(records, map[string]any{
			instrFieldKey:     in.Key,
			instrFieldNumber:  in.Number,
			instrFieldSource:  in.Source,
			instrFieldText:    in.Text,
			instrFieldNormalQ: in.NormalQuestions,
			instrFieldCriticQ: in.CriticalQuestions,
			instrFieldOnNoQ:   in.OnQuestionNotAllowed,
			instrFieldStatus:  instrPending,
			instrFieldOutcome: "",
			instrFieldStarted: "",
			instrFieldEnded:   "",
		})
	}
	data[instrStateKey] = records
}

// instrNext fails each running record of key, then starts the first pending
// one. It returns that record (nil when no record of key is pending), the
// number of pending records of key that remain after it, and the numbers of
// the running records it failed (nil when none). A state with no
// "instructions" key acts as an empty list.
func instrNext(data map[string]any, key string, now time.Time) (rec map[string]any, remaining int, failed []int, err error) {
	records, err := instrRecords(data)
	if err != nil {
		return nil, 0, nil, err
	}
	stamp := instrStamp(now)
	for _, r := range records {
		if instrString(r, instrFieldKey) != key {
			continue
		}
		switch instrString(r, instrFieldStatus) {
		case instrRunning:
			instrClose(r, instrFailed, instrOutcomeUnrecorded, stamp)
			failed = append(failed, instrInt(r, instrFieldNumber))
		case instrPending:
			if rec == nil {
				r[instrFieldStatus] = instrRunning
				r[instrFieldStarted] = stamp
				rec = r
				continue
			}
			remaining++
		}
	}
	return rec, remaining, failed, nil
}

// instrRecord closes the running record number of key with status (done,
// failed or not run) and outcome, and returns the closed record. The input
// is checked before the state.
func instrRecord(data map[string]any, key string, number int, status, outcome string, now time.Time) (map[string]any, error) {
	outcome, err := instrCheckInput(status, outcome)
	if err != nil {
		return nil, err
	}
	records, err := instrRecords(data)
	if err != nil {
		return nil, err
	}
	var keyed []map[string]any
	for _, r := range records {
		if instrString(r, instrFieldKey) == key {
			keyed = append(keyed, r)
		}
	}
	if number < 1 || number > len(keyed) {
		return nil, &mcpserver.DomainError{
			Msg:        fmt.Sprintf("instruction number %d is out of range: %s has %d instructions", number, key, len(keyed)),
			Suggestion: "Use the number from instructions-next.",
		}
	}
	target := keyed[number-1] // instrSeed numbers the records of a key 1..n in list order
	switch current := instrString(target, instrFieldStatus); current {
	case instrRunning:
	case instrPending:
		return nil, &mcpserver.DomainError{
			Msg:        fmt.Sprintf("instruction %s #%d is not running (%s)", key, number, current),
			Suggestion: "Call instructions-next to start the next open instruction.",
		}
	default:
		return nil, &mcpserver.DomainError{
			Msg:        fmt.Sprintf("instruction %s #%d is closed (%s)", key, number, current),
			Suggestion: "Call instructions-next for the next open instruction.",
		}
	}
	instrClose(target, status, outcome, instrStamp(now))
	return target, nil
}

// instrRecordAll closes each open record of key with outcome and returns the
// records it closed. A pending record becomes "not run" with the outcome. A
// running record becomes "failed" with the unrecorded outcome. With no open
// record of key it returns no record and changes nothing.
func instrRecordAll(data map[string]any, key, outcome string, now time.Time) ([]map[string]any, error) {
	outcome, err := instrCheckInput(instrNotRun, outcome)
	if err != nil {
		return nil, err
	}
	records, err := instrRecords(data)
	if err != nil {
		return nil, err
	}
	return instrCloseOpen(records, func(r map[string]any) bool {
		return instrString(r, instrFieldKey) == key
	}, outcome, instrStamp(now)), nil
}

// instrSweep closes each open record that match accepts, as instrRecordAll
// does, and returns the number of records it closed. The caller owns the
// outcome. A damaged list closes nothing and returns the DataError, so the
// caller can name the damage in its response.
func instrSweep(data map[string]any, match func(rec map[string]any) bool, outcome string, now time.Time) (int, error) {
	records, err := instrRecords(data)
	if err != nil {
		return 0, err
	}
	return len(instrCloseOpen(records, match, outcome, instrStamp(now))), nil
}

// instrSweepWarning is the warning of a sweep that found a damaged list. The
// action that sweeps still completes, so a damaged list never blocks a fail,
// a skip or a cleanup.
func instrSweepWarning(err error) string {
	return fmt.Sprintf("Instruction records not closed: %s. No record changed. %s", err.Error(), instrDamagedAdvice)
}

// instrSummary counts the records and builds the summary lines. A damaged
// list gives a zero summary and one warning.
func instrSummary(data map[string]any) (instrSummaryOut, []string) {
	out := instrSummaryOut{Lines: []string{}}
	records, err := instrRecords(data)
	if err != nil {
		return out, []string{err.Error()}
	}
	out.Total = len(records)
	for _, r := range records {
		status := instrString(r, instrFieldStatus)
		outcome := instrString(r, instrFieldOutcome)
		who := fmt.Sprintf("%s #%d (%s)", instrString(r, instrFieldKey), instrInt(r, instrFieldNumber), instrString(r, instrFieldSource))
		switch status {
		case instrDone:
			out.Done++
			if strings.HasPrefix(outcome, instrDecidedPrefix) {
				out.Lines = append(out.Lines, fmt.Sprintf("%s done, %s", who, outcome))
			}
		case instrFailed:
			out.Failed++
			out.Lines = append(out.Lines, fmt.Sprintf("%s failed: %s", who, outcome))
		case instrNotRun:
			out.NotRun++
			out.Lines = append(out.Lines, fmt.Sprintf("%s not run: %s", who, outcome))
		}
	}
	return out, nil
}

// instrRecords returns the records of data["instructions"] in list order. The
// maps are the live records, so a change to one changes data. A missing or nil
// key is an empty list. Any other shape, or a record that instrCheckRecord
// refuses, is a DataError.
func instrRecords(data map[string]any) ([]map[string]any, error) {
	raw, ok := data[instrStateKey]
	if !ok || raw == nil {
		return nil, nil
	}
	var records []map[string]any
	switch list := raw.(type) {
	case []map[string]any:
		records = list
	case []any:
		records = make([]map[string]any, 0, len(list))
		for _, item := range list {
			rec, ok := item.(map[string]any)
			if !ok {
				return nil, instrDamaged()
			}
			records = append(records, rec)
		}
	default:
		return nil, instrDamaged()
	}
	for i, rec := range records {
		if err := instrCheckRecord(i+1, rec); err != nil {
			return nil, err
		}
	}
	return records, nil
}

func instrDamaged() error {
	return &mcpserver.DataError{Msg: instrDamagedMsg, Suggestion: instrDamagedAdvice}
}

// instrCheckRecord checks the fields of record position (1-based) that decide
// an action. key, number and status are required: key a non-empty string,
// number a whole number, status one of instrStatuses. The question flags must
// be booleans and the text fields strings when present. An absent flag reads
// as false, an absent text field as "".
func instrCheckRecord(position int, rec map[string]any) error {
	bad := func(field, want string) error {
		v, present := rec[field]
		found := "absent"
		if present {
			found = fmt.Sprintf("%T %v", v, v)
		}
		return &mcpserver.DataError{
			Msg:        fmt.Sprintf("run state key instructions: record %d field %s is %s, want %s", position, field, found, want),
			Suggestion: instrDamagedAdvice,
		}
	}
	if key, ok := rec[instrFieldKey].(string); !ok || key == "" {
		return bad(instrFieldKey, "a non-empty string")
	}
	switch n := rec[instrFieldNumber].(type) {
	case int, int64:
	case float64:
		if n != float64(int(n)) {
			return bad(instrFieldNumber, "a whole number")
		}
	default:
		return bad(instrFieldNumber, "a whole number")
	}
	if status, ok := rec[instrFieldStatus].(string); !ok || !instrValidStatus(status) {
		return bad(instrFieldStatus, "one of: "+strings.Join(instrStatuses, ", "))
	}
	for _, field := range []string{instrFieldNormalQ, instrFieldCriticQ} {
		if v, present := rec[field]; present && v != nil {
			if _, ok := v.(bool); !ok {
				return bad(field, "a boolean")
			}
		}
	}
	for _, field := range []string{instrFieldSource, instrFieldText, instrFieldOnNoQ, instrFieldOutcome, instrFieldStarted, instrFieldEnded} {
		if v, present := rec[field]; present && v != nil {
			if _, ok := v.(string); !ok {
				return bad(field, "a string")
			}
		}
	}
	return nil
}

// instrCheckInput checks status and outcome and returns the trimmed outcome.
func instrCheckInput(status, outcome string) (string, error) {
	if !slices.Contains(instrClosingStatuses, status) {
		return "", &mcpserver.DomainError{
			Msg:        fmt.Sprintf("status %q is not valid", status),
			Suggestion: "Use done, failed or not run.",
		}
	}
	outcome = strings.TrimSpace(outcome)
	if n := utf8.RuneCountInString(outcome); n > MaxInstructionOutcome {
		return "", &mcpserver.DomainError{
			Msg:        fmt.Sprintf("the outcome has %d characters, over the limit of %d", n, MaxInstructionOutcome),
			Suggestion: "Shorten the outcome.",
		}
	}
	if outcome == "" && status != instrDone {
		return "", &mcpserver.DomainError{
			Msg:        "outcome is required for failed and not run",
			Suggestion: "Add an outcome that says why the instruction failed or did not run.",
		}
	}
	return outcome, nil
}

// instrCloseOpen closes each pending or running record that match accepts
// and returns the closed records in list order.
func instrCloseOpen(records []map[string]any, match func(map[string]any) bool, outcome, stamp string) []map[string]any {
	var closed []map[string]any
	for _, r := range records {
		switch instrString(r, instrFieldStatus) {
		case instrPending:
			if match(r) {
				instrClose(r, instrNotRun, outcome, stamp)
				closed = append(closed, r)
			}
		case instrRunning:
			if match(r) {
				instrClose(r, instrFailed, instrOutcomeUnrecorded, stamp)
				closed = append(closed, r)
			}
		}
	}
	return closed
}

func instrClose(rec map[string]any, status, outcome, stamp string) {
	rec[instrFieldStatus] = status
	rec[instrFieldOutcome] = outcome
	rec[instrFieldEnded] = stamp
}

func instrStamp(now time.Time) string {
	return now.UTC().Format(time.RFC3339)
}

// instrString reads a text field. instrRecords has checked the type, so a
// zero value means the field is absent.
func instrString(rec map[string]any, field string) string {
	s, _ := rec[field].(string)
	return s
}

// instrBool reads a question flag. instrRecords has checked the type, so
// false means false or absent.
func instrBool(rec map[string]any, field string) bool {
	b, _ := rec[field].(bool)
	return b
}

// instrInt reads a number that is an int in memory or a float64 after a JSON
// round trip.
func instrInt(rec map[string]any, field string) int {
	switch n := rec[field].(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	}
	return 0
}

// ---------------------------------------------------------------------------
// Shared parts of the instructions-next and instruction-record actions
// ---------------------------------------------------------------------------

// instrInputFields are the inputs that only instruction-record reads. Every
// other action refuses them, so a misrouted record call fails loud.
var instrInputFields = []string{"instructionNumber", "instructionAll", "instructionStatus", "instructionOutcome"}

// InstructionRecordOut is one instruction record as instructions-next returns
// it. The fields equal the stored record.
type InstructionRecordOut struct {
	Key                  string `json:"key"`
	Number               int    `json:"number"`
	Source               string `json:"source"`
	Text                 string `json:"text"`
	NormalQuestions      bool   `json:"normalQuestions"`
	CriticalQuestions    bool   `json:"criticalQuestions"`
	OnQuestionNotAllowed string `json:"onQuestionNotAllowed"`
	Status               string `json:"status"`
	Outcome              string `json:"outcome"`
	StartedAt            string `json:"startedAt"`
	EndedAt              string `json:"endedAt"`
}

// InstructionsNextOut is the output of instructions-next in ship_state and
// execute_state. Instruction is nil (rendered "(none)") when no record of the
// key is pending. FailedRunning holds the numbers of the records of the key
// that were still running and that this call closed as failed, with outcome
// "result not recorded, not run again"; [] when none. RunMode and Warnings are
// set by ship_state only.
type InstructionsNextOut struct {
	Summary       string                `json:"summary"`
	Instruction   *InstructionRecordOut `json:"instruction"`
	Remaining     int                   `json:"remaining"`
	FailedRunning []int                 `json:"failedRunning"`
	RunMode       string                `json:"runMode,omitempty"`
	Warnings      []string              `json:"warnings,omitempty"`
	Next          string                `json:"next"`
}

// InstructionClosedOut is one record that instruction-record closed, as
// stored after the call.
type InstructionClosedOut struct {
	Number  int    `json:"number"`
	Status  string `json:"status"`
	Outcome string `json:"outcome"`
	EndedAt string `json:"endedAt"`
}

// InstructionRecordResultOut is the output of instruction-record in
// ship_state and execute_state. Updated is the number of records the call
// closed, and Closed lists them. Closed and Warnings are [] when empty.
type InstructionRecordResultOut struct {
	Summary  string                 `json:"summary"`
	Key      string                 `json:"key"`
	Updated  int                    `json:"updated"`
	Closed   []InstructionClosedOut `json:"closed"`
	Warnings []string               `json:"warnings"`
	Next     string                 `json:"next"`
}

// instrRecordInput is the parsed input of instruction-record.
type instrRecordInput struct {
	key       string
	number    int
	hasNumber bool
	all       bool
	status    string
	outcome   string
}

// instrRefuseInputs returns a DomainError for the first record input that
// isSet reports. prefix goes before the field name in the message: "detail."
// for ship_state, "" for execute_state.
func instrRefuseInputs(isSet func(field string) bool, prefix string) error {
	for _, field := range instrInputFields {
		if isSet(field) {
			return &mcpserver.DomainError{
				Msg:        fmt.Sprintf("%s%s is valid only with instruction-record", prefix, field),
				Suggestion: fmt.Sprintf("Remove %s%s, or use action instruction-record.", prefix, field),
			}
		}
	}
	return nil
}

// check applies the rules between instructionNumber, instructionAll and
// instructionStatus. It runs before the state is read.
func (in instrRecordInput) check() error {
	switch {
	case in.hasNumber && in.all:
		return &mcpserver.DomainError{
			Msg:        "use instructionNumber or instructionAll, not both",
			Suggestion: "Send instructionNumber for one record, or instructionAll:true for each open record.",
		}
	case !in.hasNumber && !in.all:
		return &mcpserver.DomainError{
			Msg:        "instructionNumber or instructionAll is required",
			Suggestion: "Send the number from instructions-next, or instructionAll:true.",
		}
	case in.all && in.status != instrNotRun:
		return &mcpserver.DomainError{
			Msg:        `instructionAll:true takes only status "not run"`,
			Suggestion: `Send instructionStatus "not run" with instructionAll:true.`,
		}
	}
	return nil
}

// instrRecordOut copies a record into the output shape.
func instrRecordOut(rec map[string]any) *InstructionRecordOut {
	return &InstructionRecordOut{
		Key:                  instrString(rec, instrFieldKey),
		Number:               instrInt(rec, instrFieldNumber),
		Source:               instrString(rec, instrFieldSource),
		Text:                 instrString(rec, instrFieldText),
		NormalQuestions:      instrBool(rec, instrFieldNormalQ),
		CriticalQuestions:    instrBool(rec, instrFieldCriticQ),
		OnQuestionNotAllowed: instrString(rec, instrFieldOnNoQ),
		Status:               instrString(rec, instrFieldStatus),
		Outcome:              instrString(rec, instrFieldOutcome),
		StartedAt:            instrString(rec, instrFieldStarted),
		EndedAt:              instrString(rec, instrFieldEnded),
	}
}

// instrStore is the state of one instruction action: the data map, the write
// of the tool, and the InfraError that the tool returns for a failed write.
type instrStore struct {
	data       map[string]any
	write      func() error
	writeError func(error) error
}

// instrNextAction runs instructions-next for key on store. It writes the
// state when a record changed or when mustWrite is true (execute_state seeded
// the list). noneNext is the next text when no record of key is pending. A
// failed write returns the InfraError of the store.
func instrNextAction(store instrStore, key string, now time.Time, mustWrite bool, noneNext string) (InstructionsNextOut, error) {
	rec, remaining, failed, err := instrNext(store.data, key, now)
	if err != nil {
		return InstructionsNextOut{}, err
	}
	if mustWrite || rec != nil || len(failed) > 0 {
		if err := store.write(); err != nil {
			return InstructionsNextOut{}, store.writeError(err)
		}
	}
	out := InstructionsNextOut{Remaining: remaining, FailedRunning: failed}
	if out.FailedRunning == nil {
		out.FailedRunning = []int{}
	}
	if rec == nil {
		out.Summary = fmt.Sprintf("No open instruction for %s.", key)
		out.Next = noneNext
		return out, nil
	}
	out.Instruction = instrRecordOut(rec)
	out.Summary = fmt.Sprintf("%s #%d (%s) %q", key, out.Instruction.Number, out.Instruction.Source, out.Instruction.Text)
	out.Next = fmt.Sprintf("Do instruction %s #%d, then call instruction-record.", key, out.Instruction.Number)
	return out, nil
}

// instrRecordAction runs instruction-record with the checked input in on
// store. It writes the state only when a record closed. oneNext is the next
// text after one record closed; allNext is the next text after
// instructionAll:true, when no record of the key is open any more.
func instrRecordAction(store instrStore, in instrRecordInput, now time.Time, oneNext, allNext string) (InstructionRecordResultOut, error) {
	var closed []map[string]any
	if in.all {
		var err error
		if closed, err = instrRecordAll(store.data, in.key, in.outcome, now); err != nil {
			return InstructionRecordResultOut{}, err
		}
	} else {
		rec, err := instrRecord(store.data, in.key, in.number, in.status, in.outcome, now)
		if err != nil {
			return InstructionRecordResultOut{}, err
		}
		closed = []map[string]any{rec}
	}
	if len(closed) > 0 {
		if err := store.write(); err != nil {
			return InstructionRecordResultOut{}, store.writeError(err)
		}
	}
	out := InstructionRecordResultOut{
		Summary:  fmt.Sprintf("Instruction %s #%d recorded as %s.", in.key, in.number, in.status),
		Key:      in.key,
		Updated:  len(closed),
		Closed:   make([]InstructionClosedOut, 0, len(closed)),
		Warnings: []string{},
		Next:     oneNext,
	}
	for _, r := range closed {
		out.Closed = append(out.Closed, InstructionClosedOut{
			Number:  instrInt(r, instrFieldNumber),
			Status:  instrString(r, instrFieldStatus),
			Outcome: instrString(r, instrFieldOutcome),
			EndedAt: instrString(r, instrFieldEnded),
		})
	}
	if in.all {
		out.Summary = fmt.Sprintf("%d open instructions of %s closed.", len(closed), in.key)
		out.Next = allNext
	}
	return out, nil
}
