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
package tools

import (
	"fmt"
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
	instrFieldKey      = "key"
	instrFieldNumber   = "number"
	instrFieldSource   = "source"
	instrFieldText     = "text"
	instrFieldNormalQ  = "normalQuestions"
	instrFieldCriticQ  = "criticalQuestions"
	instrFieldOnNoQ    = "onQuestionNotAllowed"
	instrFieldStatus   = "status"
	instrFieldOutcome  = "outcome"
	instrFieldStarted  = "startedAt"
	instrFieldEnded    = "endedAt"
	instrDamagedMsg    = "run state key instructions is not a list of records"
	instrDamagedAdvice = "Start a new run. The run state is damaged."
)

// instrSummaryOut is the summary of the records of one run. Total counts every
// record. Lines holds one line for each failed, not run or decided record.
type instrSummaryOut struct {
	Total, Done, Failed, NotRun int
	Lines                       []string // one per failed, not run, or decided record
}

// instrSeed writes one pending record for each instruction of list, replacing
// any earlier records in data. The 3 question flags come from the config
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
// one. It returns that record (nil when no record of key is pending) and the
// number of pending records of key that remain after it. A state with no
// "instructions" key acts as an empty list.
func instrNext(data map[string]any, key string, now time.Time) (rec map[string]any, remaining int, err error) {
	records, err := instrRecords(data)
	if err != nil {
		return nil, 0, err
	}
	stamp := instrStamp(now)
	for _, r := range records {
		if instrString(r, instrFieldKey) == key && instrString(r, instrFieldStatus) == instrRunning {
			instrClose(r, instrFailed, instrOutcomeUnrecorded, stamp)
		}
	}
	for _, r := range records {
		if instrString(r, instrFieldKey) != key || instrString(r, instrFieldStatus) != instrPending {
			continue
		}
		if rec == nil {
			r[instrFieldStatus] = instrRunning
			r[instrFieldStarted] = stamp
			rec = r
			continue
		}
		remaining++
	}
	return rec, remaining, nil
}

// instrRecord closes the running record number of key with status (done,
// failed or not run) and outcome. The input is checked before the state.
func instrRecord(data map[string]any, key string, number int, status, outcome string, now time.Time) error {
	outcome, err := instrCheckInput(status, outcome)
	if err != nil {
		return err
	}
	records, err := instrRecords(data)
	if err != nil {
		return err
	}
	var keyed []map[string]any
	for _, r := range records {
		if instrString(r, instrFieldKey) == key {
			keyed = append(keyed, r)
		}
	}
	if number < 1 || number > len(keyed) {
		return &mcpserver.DomainError{
			Msg:        fmt.Sprintf("%s has %d instructions. Number %d is out of range.", key, len(keyed), number),
			Suggestion: "Use the number from instructions-next.",
		}
	}
	target := keyed[number-1] // numbers run 1..n in list order
	for _, r := range keyed {
		if instrInt(r, instrFieldNumber) == number {
			target = r
			break
		}
	}
	switch current := instrString(target, instrFieldStatus); current {
	case instrRunning:
	case instrPending:
		return &mcpserver.DomainError{
			Msg:        fmt.Sprintf("instruction %s #%d is not running (%s)", key, number, current),
			Suggestion: "Call instructions-next to start the next open instruction.",
		}
	default:
		return &mcpserver.DomainError{
			Msg:        fmt.Sprintf("instruction %s #%d is closed (%s)", key, number, current),
			Suggestion: "Call instructions-next for the next open instruction.",
		}
	}
	instrClose(target, status, outcome, instrStamp(now))
	return nil
}

// instrRecordAll closes each open record of key with outcome and returns the
// number of records it closed. A pending record becomes "not run" with the
// outcome. A running record becomes "failed" with the unrecorded outcome.
// With no open record of key it returns 0 and writes nothing.
func instrRecordAll(data map[string]any, key, outcome string, now time.Time) (updated int, err error) {
	outcome, err = instrCheckInput(instrNotRun, outcome)
	if err != nil {
		return 0, err
	}
	records, err := instrRecords(data)
	if err != nil {
		return 0, err
	}
	return instrCloseOpen(records, func(r map[string]any) bool {
		return instrString(r, instrFieldKey) == key
	}, outcome, instrStamp(now)), nil
}

// instrSweep closes each open record that match accepts, as instrRecordAll
// does, and returns the number of records it closed. The caller owns the
// outcome. A damaged list closes nothing and returns 0.
func instrSweep(data map[string]any, match func(rec map[string]any) bool, outcome string, now time.Time) int {
	records, err := instrRecords(data)
	if err != nil {
		return 0
	}
	return instrCloseOpen(records, match, outcome, instrStamp(now))
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
// key is an empty list. Any other shape is a DataError.
func instrRecords(data map[string]any) ([]map[string]any, error) {
	raw, ok := data[instrStateKey]
	if !ok || raw == nil {
		return nil, nil
	}
	switch list := raw.(type) {
	case []map[string]any:
		return list, nil
	case []any:
		records := make([]map[string]any, 0, len(list))
		for _, item := range list {
			rec, ok := item.(map[string]any)
			if !ok {
				return nil, instrDamaged()
			}
			records = append(records, rec)
		}
		return records, nil
	default:
		return nil, instrDamaged()
	}
}

func instrDamaged() error {
	return &mcpserver.DataError{Msg: instrDamagedMsg, Suggestion: instrDamagedAdvice}
}

// instrCheckInput checks status and outcome and returns the trimmed outcome.
func instrCheckInput(status, outcome string) (string, error) {
	switch status {
	case instrDone, instrFailed, instrNotRun:
	default:
		return "", &mcpserver.DomainError{
			Msg:        fmt.Sprintf("status %q is not valid", status),
			Suggestion: "Use done, failed or not run.",
		}
	}
	outcome = strings.TrimSpace(outcome)
	if n := utf8.RuneCountInString(outcome); n > MaxInstructionOutcome {
		return "", &mcpserver.DomainError{
			Msg:        fmt.Sprintf("the outcome has %d characters. The limit is %d.", n, MaxInstructionOutcome),
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

// instrCloseOpen closes each pending or running record that match accepts.
func instrCloseOpen(records []map[string]any, match func(map[string]any) bool, outcome, stamp string) int {
	updated := 0
	for _, r := range records {
		switch instrString(r, instrFieldStatus) {
		case instrPending:
			if match(r) {
				instrClose(r, instrNotRun, outcome, stamp)
				updated++
			}
		case instrRunning:
			if match(r) {
				instrClose(r, instrFailed, instrOutcomeUnrecorded, stamp)
				updated++
			}
		}
	}
	return updated
}

func instrClose(rec map[string]any, status, outcome, stamp string) {
	rec[instrFieldStatus] = status
	rec[instrFieldOutcome] = outcome
	rec[instrFieldEnded] = stamp
}

func instrStamp(now time.Time) string {
	return now.UTC().Format(time.RFC3339)
}

func instrString(rec map[string]any, field string) string {
	s, _ := rec[field].(string)
	return s
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
