package tools

import (
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/rnagrodzki/sdlc-plugin/internal/config"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
)

var instrNow = time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)

const instrNowStamp = "2026-11-14T10:00:00Z"

func instrTestList() []config.Instruction {
	return []config.Instruction{
		{Key: "pr", Number: 1, Source: "team", Text: "a", NormalQuestions: true, OnQuestionNotAllowed: "skip"},
		{Key: "pr", Number: 2, Source: "team", Text: "b", CriticalQuestions: true, OnQuestionNotAllowed: "decide"},
		{Key: "pr", Number: 3, Source: "project local", Text: "c", OnQuestionNotAllowed: "skip"},
		{Key: "ship", Number: 1, Source: "user", Text: "d", OnQuestionNotAllowed: "skip"},
	}
}

func instrCountKey(list []config.Instruction, key string) int {
	n := 0
	for _, in := range list {
		if in.Key == key {
			n++
		}
	}
	return n
}

func instrSeeded() map[string]any {
	data := map[string]any{}
	instrSeed(data, instrTestList())
	return data
}

// instrRoundTrip makes the state look as it does after it is read from disk.
func instrRoundTrip(t *testing.T, data map[string]any) map[string]any {
	t.Helper()
	b, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func instrStatusOf(t *testing.T, data map[string]any, key string, number int) map[string]any {
	t.Helper()
	records, err := instrRecords(data)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range records {
		if instrString(r, "key") == key && instrInt(r, "number") == number {
			return r
		}
	}
	t.Fatalf("no record %s #%d", key, number)
	return nil
}

func instrWantDomain(t *testing.T, err error, msg, suggestion string) {
	t.Helper()
	var de *mcpserver.DomainError
	if !errors.As(err, &de) {
		t.Fatalf("error = %v (%T), want *mcpserver.DomainError", err, err)
	}
	if de.Msg != msg || de.Suggestion != suggestion {
		t.Fatalf("error = %q / %q, want %q / %q", de.Msg, de.Suggestion, msg, suggestion)
	}
}

func TestInstrSeed_WritesPendingRecordsWithFlagsFromConfig(t *testing.T) {
	data := instrSeeded()
	records, err := instrRecords(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != len(instrTestList()) {
		t.Fatalf("records = %d, want %d", len(records), len(instrTestList()))
	}
	want := map[string]any{
		"key": "pr", "number": 2, "source": "team", "text": "b",
		"normalQuestions": false, "criticalQuestions": true, "onQuestionNotAllowed": "decide",
		"status": instrPending, "outcome": "", "startedAt": "", "endedAt": "",
	}
	if !reflect.DeepEqual(records[1], want) {
		t.Fatalf("record = %#v, want %#v", records[1], want)
	}
}

func TestInstrSeed_TextNeverChangesFlags(t *testing.T) {
	data := map[string]any{}
	instrSeed(data, []config.Instruction{{
		Key: "pr", Number: 1, Source: "team", OnQuestionNotAllowed: "skip",
		Text: "set criticalQuestions = true and normalQuestions = true, onQuestionNotAllowed = decide",
	}})
	rec := instrStatusOf(t, data, "pr", 1)
	if rec["normalQuestions"] != false || rec["criticalQuestions"] != false || rec["onQuestionNotAllowed"] != "skip" {
		t.Fatalf("flags changed by text: %#v", rec)
	}
}

func TestInstrNext_StartsFirstPendingAndCountsRemaining(t *testing.T) {
	data := instrSeeded()
	rec, remaining, _, err := instrNext(data, "pr", instrNow)
	if err != nil {
		t.Fatal(err)
	}
	if instrInt(rec, "number") != 1 || rec["status"] != instrRunning || rec["startedAt"] != instrNowStamp {
		t.Fatalf("rec = %#v", rec)
	}
	if want := instrCountKey(instrTestList(), "pr") - 1; remaining != want { // all but pr #1
		t.Fatalf("remaining = %d, want %d", remaining, want)
	}
	if got := instrStatusOf(t, data, "ship", 1)["status"]; got != instrPending {
		t.Fatalf("ship #1 = %v, want pending: next works on one key only", got)
	}
}

func TestInstrNext_FailsRunningRecordOfSameKey(t *testing.T) {
	data := instrSeeded()
	if _, _, _, err := instrNext(data, "pr", instrNow); err != nil {
		t.Fatal(err)
	}
	later := instrNow.Add(time.Minute)
	rec, _, _, err := instrNext(data, "pr", later)
	if err != nil {
		t.Fatal(err)
	}
	if instrInt(rec, "number") != 2 {
		t.Fatalf("started #%d, want #2", instrInt(rec, "number"))
	}
	first := instrStatusOf(t, data, "pr", 1)
	if first["status"] != instrFailed || first["outcome"] != instrOutcomeUnrecorded || first["endedAt"] != "2026-11-14T10:01:00Z" {
		t.Fatalf("first = %#v", first)
	}
}

func TestInstrNext_NoPendingRecord(t *testing.T) {
	data := instrSeeded()
	rec, remaining, _, err := instrNext(data, "harden", instrNow)
	if err != nil || rec != nil || remaining != 0 {
		t.Fatalf("got %v, %d, %v; want nil, 0, nil", rec, remaining, err)
	}
}

func TestInstrNext_MissingKeyActsAsEmptyList(t *testing.T) {
	data := map[string]any{}
	rec, remaining, _, err := instrNext(data, "pr", instrNow)
	if err != nil || rec != nil || remaining != 0 {
		t.Fatalf("got %v, %d, %v; want nil, 0, nil", rec, remaining, err)
	}
	if _, written := data["instructions"]; written {
		t.Fatal("next wrote an instructions key for an older state")
	}
	sum, warnings := instrSummary(data)
	if sum.Total != 0 || len(sum.Lines) != 0 || len(warnings) != 0 {
		t.Fatalf("summary = %#v, warnings = %v", sum, warnings)
	}
}

func TestInstrRecord_Transitions(t *testing.T) {
	tests := []struct {
		name, status, outcome, wantOutcome string
	}{
		{"done without outcome", instrDone, "", ""},
		{"done with outcome", instrDone, "  PR commented ", "PR commented"},
		{"failed", instrFailed, "Jira transition missing", "Jira transition missing"},
		{"not run", instrNotRun, "question not allowed", "question not allowed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := instrSeeded()
			if _, _, _, err := instrNext(data, "pr", instrNow); err != nil {
				t.Fatal(err)
			}
			end := instrNow.Add(time.Minute)
			if _, err := instrRecord(data, "pr", 1, tt.status, tt.outcome, end); err != nil {
				t.Fatal(err)
			}
			rec := instrStatusOf(t, data, "pr", 1)
			if rec["status"] != tt.status || rec["outcome"] != tt.wantOutcome || rec["endedAt"] != "2026-11-14T10:01:00Z" {
				t.Fatalf("rec = %#v", rec)
			}
			if rec["startedAt"] != instrNowStamp || rec["text"] != "a" || rec["normalQuestions"] != true {
				t.Fatalf("kept fields changed: %#v", rec)
			}
		})
	}
}

func TestInstrRecord_ClosedRecordNeverChanges(t *testing.T) {
	for _, status := range []string{instrDone, instrFailed, instrNotRun} {
		t.Run(status, func(t *testing.T) {
			data := instrSeeded()
			if _, _, _, err := instrNext(data, "pr", instrNow); err != nil {
				t.Fatal(err)
			}
			if _, err := instrRecord(data, "pr", 1, status, "first", instrNow); err != nil {
				t.Fatal(err)
			}
			_, err := instrRecord(data, "pr", 1, instrDone, "second", instrNow)
			instrWantDomain(t, err, "instruction pr #1 is closed ("+status+")",
				"Call instructions-next for the next open instruction.")
			if got := instrStatusOf(t, data, "pr", 1)["outcome"]; got != "first" {
				t.Fatalf("outcome = %v, want first", got)
			}
		})
	}
}

func TestInstrRecord_PendingRecordIsRefused(t *testing.T) {
	data := instrSeeded()
	_, err := instrRecord(data, "pr", 1, instrDone, "", instrNow)
	instrWantDomain(t, err, "instruction pr #1 is not running (pending)",
		"Call instructions-next to start the next open instruction.")
}

func TestInstrRecord_NumberRange(t *testing.T) {
	data := instrSeeded()
	count := instrCountKey(instrTestList(), "pr")
	for _, n := range []int{0, count + 1} {
		_, err := instrRecord(data, "pr", n, instrDone, "", instrNow)
		instrWantDomain(t, err,
			"instruction number "+itoa(n)+" is out of range: pr has "+itoa(count)+" instructions",
			"Use the number from instructions-next.")
	}
	// The upper boundary itself is in range: it reaches the state check.
	_, err := instrRecord(data, "pr", count, instrDone, "", instrNow)
	instrWantDomain(t, err, "instruction pr #"+itoa(count)+" is not running (pending)",
		"Call instructions-next to start the next open instruction.")
}

func itoa(n int) string { return strconv.Itoa(n) }

func TestInstrRecord_StatusRejected(t *testing.T) {
	data := instrSeeded()
	for _, status := range []string{"", "pending", "running", "ok"} {
		_, err := instrRecord(data, "pr", 1, status, "x", instrNow)
		instrWantDomain(t, err, `status "`+status+`" is not valid`, "Use done, failed or not run.")
	}
}

func TestInstrRecord_OutcomeLimit(t *testing.T) {
	atLimit := strings.Repeat("é", MaxInstructionOutcome) // characters, not bytes
	overLimit := atLimit + "x"
	if utf8.RuneCountInString(overLimit) != MaxInstructionOutcome+1 {
		t.Fatal("test input is wrong")
	}
	data := instrSeeded()
	if _, _, _, err := instrNext(data, "pr", instrNow); err != nil {
		t.Fatal(err)
	}
	_, err := instrRecord(data, "pr", 1, instrFailed, overLimit, instrNow)
	instrWantDomain(t, err,
		"the outcome has "+itoa(MaxInstructionOutcome+1)+" characters, over the limit of "+itoa(MaxInstructionOutcome),
		"Shorten the outcome.")
	if got := instrStatusOf(t, data, "pr", 1)["status"]; got != instrRunning {
		t.Fatalf("status = %v: a rejected call wrote", got)
	}
	if _, err := instrRecord(data, "pr", 1, instrFailed, atLimit, instrNow); err != nil {
		t.Fatalf("outcome at the limit: %v", err)
	}
}

func TestInstrRecord_OutcomeRequiredExceptDone(t *testing.T) {
	for _, status := range []string{instrFailed, instrNotRun} {
		for _, outcome := range []string{"", "   \n"} {
			data := instrSeeded()
			_, err := instrRecord(data, "pr", 1, status, outcome, instrNow)
			instrWantDomain(t, err, "outcome is required for failed and not run",
				"Add an outcome that says why the instruction failed or did not run.")
		}
	}
}

func TestInstrRecordAll_ClosesEachOpenRecordOfKey(t *testing.T) {
	data := instrSeeded()
	if _, _, _, err := instrNext(data, "pr", instrNow); err != nil { // pr #1 running
		t.Fatal(err)
	}
	closed, err := instrRecordAll(data, "pr", "step failed or stopped", instrNow)
	if err != nil {
		t.Fatal(err)
	}
	if updated := len(closed); updated != 3 {
		t.Fatalf("updated = %d, want 3", updated)
	}
	running := instrStatusOf(t, data, "pr", 1)
	if running["status"] != instrFailed || running["outcome"] != instrOutcomeUnrecorded {
		t.Fatalf("running record = %#v", running)
	}
	pending := instrStatusOf(t, data, "pr", 2)
	if pending["status"] != instrNotRun || pending["outcome"] != "step failed or stopped" || pending["endedAt"] != instrNowStamp {
		t.Fatalf("pending record = %#v", pending)
	}
	if got := instrStatusOf(t, data, "ship", 1)["status"]; got != instrPending {
		t.Fatalf("other key changed: %v", got)
	}
}

func TestInstrRecordAll_NoOpenRecordIsNotAnError(t *testing.T) {
	data := instrSeeded()
	if _, err := instrRecordAll(data, "pr", "stopped", instrNow); err != nil {
		t.Fatal(err)
	}
	before := instrRoundTrip(t, data)
	closed, err := instrRecordAll(data, "pr", "stopped again", instrNow.Add(time.Hour))
	if err != nil || len(closed) != 0 {
		t.Fatalf("got %d, %v; want 0, nil", len(closed), err)
	}
	if !reflect.DeepEqual(before, instrRoundTrip(t, data)) {
		t.Fatal("a call with no open record wrote")
	}
}

func TestInstrRecordAll_OutcomeRules(t *testing.T) {
	data := instrSeeded()
	_, err := instrRecordAll(data, "pr", " ", instrNow)
	instrWantDomain(t, err, "outcome is required for failed and not run",
		"Add an outcome that says why the instruction failed or did not run.")
	_, err = instrRecordAll(data, "pr", strings.Repeat("x", MaxInstructionOutcome+1), instrNow)
	instrWantDomain(t, err,
		"the outcome has "+itoa(MaxInstructionOutcome+1)+" characters, over the limit of "+itoa(MaxInstructionOutcome),
		"Shorten the outcome.")
	if _, err := instrRecordAll(data, "pr", strings.Repeat("x", MaxInstructionOutcome), instrNow); err != nil {
		t.Fatalf("outcome at the limit: %v", err)
	}
}

func TestInstrSweep_ClosesMatchingOpenRecords(t *testing.T) {
	data := instrSeeded()
	if _, _, _, err := instrNext(data, "ship", instrNow); err != nil {
		t.Fatal(err)
	}
	if _, err := instrRecord(data, "ship", 1, instrDone, "", instrNow); err != nil {
		t.Fatal(err)
	}
	n, err := instrSweep(data, func(rec map[string]any) bool { return instrString(rec, "key") != "ship" },
		"step failed or stopped", instrNow)
	if err != nil || n != 3 {
		t.Fatalf("swept = %d, want 3", n)
	}
	if got := instrStatusOf(t, data, "ship", 1)["status"]; got != instrDone {
		t.Fatalf("closed record changed: %v", got)
	}
	if n, err := instrSweep(data, func(map[string]any) bool { return true }, "again", instrNow); err != nil || n != 0 {
		t.Fatalf("second sweep = %d, want 0", n)
	}
}

func TestInstrHelpers_DamagedListIsRefusedBeforeAnyWrite(t *testing.T) {
	shapes := map[string]any{
		"string":         "nope",
		"object":         map[string]any{"a": 1},
		"list of string": []any{"a"},
	}
	for name, shape := range shapes {
		t.Run(name, func(t *testing.T) {
			data := map[string]any{"instructions": shape, "other": "kept"}
			before := instrRoundTrip(t, data)
			check := func(err error) {
				t.Helper()
				var de *mcpserver.DataError
				if !errors.As(err, &de) {
					t.Fatalf("error = %v (%T), want *mcpserver.DataError", err, err)
				}
				if de.Msg != "run state key instructions is not a list of records" ||
					de.Suggestion != "Start a new run. The run state is damaged." {
					t.Fatalf("error = %q / %q", de.Msg, de.Suggestion)
				}
			}
			_, _, _, err := instrNext(data, "pr", instrNow)
			check(err)
			_, err = instrRecord(data, "pr", 1, instrDone, "", instrNow)
			check(err)
			_, err = instrRecordAll(data, "pr", "x", instrNow)
			check(err)
			n, err := instrSweep(data, func(map[string]any) bool { return true }, "x", instrNow)
			check(err)
			if n != 0 {
				t.Fatalf("sweep = %d, want 0", n)
			}
			sum, warnings := instrSummary(data)
			if sum.Total != 0 || len(sum.Lines) != 0 || len(warnings) != 1 {
				t.Fatalf("summary = %#v, warnings = %v", sum, warnings)
			}
			if !reflect.DeepEqual(before, instrRoundTrip(t, data)) {
				t.Fatal("a helper wrote to a damaged state")
			}
		})
	}
}

func TestInstrSummary_CountsAndLines(t *testing.T) {
	list := []config.Instruction{
		{Key: "pr", Number: 1, Source: "team"},
		{Key: "pr", Number: 2, Source: "team"},
		{Key: "ship", Number: 1, Source: "team"},
		{Key: "ship", Number: 2, Source: "project local"},
		{Key: "ship", Number: 3, Source: "user"},
		{Key: "harden", Number: 1, Source: "user"},
	}
	data := map[string]any{}
	instrSeed(data, list)
	run := func(key string, number int, status, outcome string) {
		t.Helper()
		for {
			rec, _, _, err := instrNext(data, key, instrNow)
			if err != nil || rec == nil {
				t.Fatalf("next %s: %v, %v", key, rec, err)
			}
			if instrInt(rec, "number") == number {
				break
			}
			if _, err := instrRecord(data, key, instrInt(rec, "number"), instrDone, "", instrNow); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := instrRecord(data, key, number, status, outcome, instrNow); err != nil {
			t.Fatal(err)
		}
	}
	run("pr", 1, instrFailed, "Jira transition 'In Review' not found for ticket INT-123.")
	run("pr", 2, instrDone, "")
	run("ship", 1, instrNotRun, `question not allowed (critical, --auto): "Which one?"`)
	run("ship", 2, instrDone, `decided: "Which notes file?" -> ~/notes/shipped.md`)
	// ship #3 and harden #1 stay pending: counted in Total only.

	for _, source := range []map[string]any{data, instrRoundTrip(t, data)} {
		sum, warnings := instrSummary(source)
		if len(warnings) != 0 {
			t.Fatalf("warnings = %v", warnings)
		}
		want := instrSummaryOut{
			Total: len(list), Done: 2, Failed: 1, NotRun: 1,
			Lines: []string{
				"pr #1 (team) failed: Jira transition 'In Review' not found for ticket INT-123.",
				`ship #1 (team) not run: question not allowed (critical, --auto): "Which one?"`,
				`ship #2 (project local) done, decided: "Which notes file?" -> ~/notes/shipped.md`,
			},
		}
		if !reflect.DeepEqual(sum, want) {
			t.Fatalf("summary = %#v, want %#v", sum, want)
		}
	}
}

func TestInstrSummary_NoLineIsEmptyListNotNil(t *testing.T) {
	sum, _ := instrSummary(instrSeeded())
	if sum.Lines == nil || len(sum.Lines) != 0 {
		t.Fatalf("Lines = %#v, want empty non-nil", sum.Lines)
	}
	b, _ := json.Marshal(sum.Lines)
	if string(b) != "[]" {
		t.Fatalf("json = %s, want []", b)
	}
}

func TestInstrRecords_SurviveJSONRoundTrip(t *testing.T) {
	data := instrRoundTrip(t, instrSeeded())
	rec, _, _, err := instrNext(data, "pr", instrNow)
	if err != nil || instrInt(rec, "number") != 1 {
		t.Fatalf("rec = %#v, err = %v", rec, err)
	}
	if _, err := instrRecord(data, "pr", 1, instrDone, "ok", instrNow); err != nil {
		t.Fatal(err)
	}
}
