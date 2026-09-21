package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rnagrodzki/sdlc-plugin/internal/history"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
	"github.com/rnagrodzki/sdlc-plugin/internal/pipeline"
)

const shipErrBranch = "feat/errs"

var shipErrNow = fixedNow(time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC))

type shipErrKind string

const (
	shipErrDomain shipErrKind = "domain"
	shipErrInfra  shipErrKind = "infra"
	shipErrData   shipErrKind = "data"
)

// requireShipErr checks the concrete error type; callers assert only stable substrings of the text it returns.
func requireShipErr(t *testing.T, err error, want shipErrKind) (string, error) {
	t.Helper()
	if err == nil {
		t.Fatalf("got nil error, want a %s error", want)
	}
	var (
		domainErr *mcpserver.DomainError
		infraErr  *mcpserver.InfraError
		dataErr   *mcpserver.DataError
		got       shipErrKind
		msg       string
		sugg      string
		cause     error
	)
	switch {
	case errors.As(err, &domainErr):
		got, msg, sugg, cause = shipErrDomain, domainErr.Msg, domainErr.Suggestion, domainErr.Cause
	case errors.As(err, &infraErr):
		got, msg, sugg, cause = shipErrInfra, infraErr.Msg, infraErr.Suggestion, infraErr.Cause
	case errors.As(err, &dataErr):
		got, msg, sugg, cause = shipErrData, dataErr.Msg, dataErr.Suggestion, dataErr.Cause
	default:
		t.Fatalf("got %T (%v), want a %s error", err, err, want)
	}
	if got != want {
		t.Fatalf("got a %s error (%v), want a %s error", got, err, want)
	}
	if msg == "" {
		t.Errorf("%s error has an empty Msg", got)
	}
	if got == shipErrInfra && cause == nil {
		t.Errorf("infra error has no Cause: %v", err)
	}
	return msg + "\n" + sugg, cause
}

func shipErrReadFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return b
}

func shipErrWithBranch(detail map[string]any) map[string]any {
	out := map[string]any{"branch": shipErrBranch}
	for k, v := range detail {
		out[k] = v
	}
	return out
}

// ---------------------------------------------------------------------------
// Required fields: rejected before any I/O
// ---------------------------------------------------------------------------

func TestShipState_StepRequired(t *testing.T) {
	root := t.TempDir()
	for _, action := range []string{"start", "complete", "begin-step", "complete-step", "skip", "fail", "decide"} {
		t.Run(action, func(t *testing.T) {
			_, err := shipState(root, root, ShipStateIn{
				Action: action,
				Detail: map[string]any{"branch": shipErrBranch},
			}, shipErrNow)
			text, _ := requireShipErr(t, err, shipErrDomain)
			if !strings.Contains(text, "step") {
				t.Errorf("error text = %q, want it to name the step field", text)
			}
		})
	}
}

func TestShipState_HistoryActions_MissingFields(t *testing.T) {
	tests := []struct {
		name string
		in   ShipStateIn
		want string
	}{
		{"history_record without outcome", ShipStateIn{Action: "history_record", Detail: map[string]any{"skill": "ship"}}, "detail.outcome"},
		{"deferred_add without detail", ShipStateIn{Action: "deferred_add"}, "detail.id"},
		{"deferred_resolve without detail", ShipStateIn{Action: "deferred_resolve"}, "detail.id"},
		{"deferred_resolve with empty id", ShipStateIn{Action: "deferred_resolve", Detail: map[string]any{"id": ""}}, "detail.id"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			_, err := shipState(root, root, tt.in, shipErrNow)
			text, _ := requireShipErr(t, err, shipErrDomain)
			if !strings.Contains(text, tt.want) {
				t.Errorf("error text = %q, want it to contain %q", text, tt.want)
			}
			if _, statErr := os.Stat(historyDir(root)); !os.IsNotExist(statErr) {
				t.Errorf("history dir was touched before validation failed: stat err = %v", statErr)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// State lookup
// ---------------------------------------------------------------------------

func TestShipState_StepNotFound(t *testing.T) {
	dir := t.TempDir()
	path := shipStateInitFixture(t, dir, shipErrBranch)
	before := shipErrReadFile(t, path)

	for _, action := range []string{"start", "complete", "begin-step", "complete-step", "skip", "fail"} {
		t.Run(action, func(t *testing.T) {
			_, err := shipState(dir, dir, ShipStateIn{
				Action: action,
				Step:   "bogus",
				Detail: map[string]any{"branch": shipErrBranch},
			}, shipErrNow)
			text, _ := requireShipErr(t, err, shipErrData)
			if !strings.Contains(text, "bogus") {
				t.Errorf("error text = %q, want it to name the missing step", text)
			}
			if after := shipErrReadFile(t, path); !bytes.Equal(before, after) {
				t.Error("state file changed although the step was not found")
			}
		})
	}
}

func TestShipState_NoStateForBranch(t *testing.T) {
	tests := []ShipStateIn{
		{Action: "start", Step: "execute"},
		{Action: "complete", Step: "execute"},
		{Action: "begin-step", Step: "execute"},
		{Action: "complete-step", Step: "execute"},
		{Action: "skip", Step: "execute"},
		{Action: "fail", Step: "execute"},
		{Action: "decide", Step: "execute"},
		{Action: "defer", Detail: map[string]any{"severity": "low", "file": "a.go", "title": "t"}},
		{Action: "read"},
		{Action: "next"},
		{Action: "todos"},
	}
	for _, in := range tests {
		t.Run(in.Action, func(t *testing.T) {
			dir := t.TempDir()
			in.Detail = shipErrWithBranch(in.Detail)
			_, err := shipState(dir, dir, in, shipErrNow)
			text, _ := requireShipErr(t, err, shipErrData)
			if !strings.Contains(text, shipErrBranch) {
				t.Errorf("error text = %q, want it to name branch %q", text, shipErrBranch)
			}
		})
	}
}

func TestShipState_BranchUnresolvable(t *testing.T) {
	// Cap git's upward search so "git branch --show-current" fails wherever TMPDIR lives.
	dir := t.TempDir()
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(dir))

	tests := []ShipStateIn{
		{Action: "init"},
		{Action: "start", Step: "execute"},
		{Action: "complete", Step: "execute"},
		{Action: "begin-step", Step: "execute"},
		{Action: "complete-step", Step: "execute"},
		{Action: "skip", Step: "execute"},
		{Action: "fail", Step: "execute"},
		{Action: "decide", Step: "execute"},
		{Action: "defer", Detail: map[string]any{"severity": "low", "file": "a.go", "title": "t"}},
		{Action: "read"},
		{Action: "next"},
		{Action: "todos"},
		{Action: "cleanup"},
		{Action: "cleanup-pipeline"},
	}
	for _, in := range tests {
		t.Run(in.Action, func(t *testing.T) {
			_, err := shipState(dir, dir, in, shipErrNow)
			requireShipErr(t, err, shipErrDomain)
		})
	}
}

func TestShipState_StateFileLoadErrors(t *testing.T) {
	dir := t.TempDir()
	corrupt := filepath.Join(dir, "corrupt.json")
	writeFile(t, corrupt, "{not json")
	asDir := filepath.Join(dir, "is-a-dir.json")
	if err := os.Mkdir(asDir, 0o755); err != nil {
		t.Fatal(err)
	}
	files := []struct {
		name string
		path string
		want shipErrKind
	}{
		{"missing file", filepath.Join(dir, "missing.json"), shipErrData},
		{"unparsable file", corrupt, shipErrDomain},
		{"path is a directory", asDir, shipErrInfra},
	}
	actions := []ShipStateIn{
		{Action: "begin-step", Step: "execute"},
		{Action: "complete-step", Step: "execute"},
		{Action: "next"},
		{Action: "todos"},
	}
	for _, f := range files {
		for _, in := range actions {
			t.Run(f.name+"/"+in.Action, func(t *testing.T) {
				in.Detail = map[string]any{"stateFile": f.path}
				_, err := shipState(dir, dir, in, shipErrNow)
				text, _ := requireShipErr(t, err, f.want)
				if !strings.Contains(text, f.path) {
					t.Errorf("error text = %q, want it to name %s", text, f.path)
				}
			})
		}
	}
}

func TestShipState_Next_SkipsNonMapSteps(t *testing.T) {
	dir := t.TempDir()
	stateFile := filepath.Join(dir, "state.json")
	writeFile(t, stateFile, `{"steps": ["not-a-step", {"name": "execute", "status": "pending"}]}`)

	out, err := shipState(dir, dir, ShipStateIn{
		Action: "next",
		Detail: map[string]any{"stateFile": stateFile},
	}, shipErrNow)
	if err != nil {
		t.Fatalf("next: %v", err)
	}
	got, ok := out.(ShipNextOut)
	if !ok {
		t.Fatalf("output = %#v, want ShipNextOut", out)
	}
	if got.Step != "execute" {
		t.Errorf("next step = %q, want execute", got.Step)
	}
}

// ---------------------------------------------------------------------------
// history_record / deferred_*: storage failures that need no permission bits
// ---------------------------------------------------------------------------

func TestShipState_HistoryStorageFailures(t *testing.T) {
	const (
		historyIsFile   = "history path is a regular file"
		corruptDeferred = "deferred.json is corrupt"
	)
	tests := []struct {
		name  string
		setup string
		in    ShipStateIn
	}{
		{"history_record", historyIsFile, ShipStateIn{Action: "history_record", Detail: map[string]any{"skill": "ship", "outcome": "success"}}},
		{"deferred_add", historyIsFile, ShipStateIn{Action: "deferred_add", Detail: map[string]any{"id": "d1", "description": "later"}}},
		{"deferred_list", historyIsFile, ShipStateIn{Action: "deferred_list"}},
		{"deferred_propose_followups", historyIsFile, ShipStateIn{Action: "deferred_propose_followups"}},
		{"deferred_resolve", historyIsFile, ShipStateIn{Action: "deferred_resolve", Detail: map[string]any{"id": "d1"}}},
		{"deferred_add on corrupt file", corruptDeferred, ShipStateIn{Action: "deferred_add", Detail: map[string]any{"id": "d1", "description": "later"}}},
		{"deferred_list on corrupt file", corruptDeferred, ShipStateIn{Action: "deferred_list"}},
		{"deferred_propose_followups on corrupt file", corruptDeferred, ShipStateIn{Action: "deferred_propose_followups"}},
		{"deferred_resolve on corrupt file", corruptDeferred, ShipStateIn{Action: "deferred_resolve", Detail: map[string]any{"id": "d1"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			switch tt.setup {
			case historyIsFile:
				writeFile(t, historyDir(root), "not a directory")
			case corruptDeferred:
				writeFile(t, filepath.Join(historyDir(root), "deferred.json"), "{garbage")
			}
			_, err := shipState(root, root, tt.in, shipErrNow)
			requireShipErr(t, err, shipErrInfra)
		})
	}
}

func TestShipState_HistoryRecord_DetailValueTypes(t *testing.T) {
	tests := []struct {
		name     string
		duration any
		want     int64
	}{
		{"float64 as decoded from JSON", float64(1500), 1500},
		{"int64", int64(7), 7},
		{"json.Number", json.Number("42"), 42},
		{"unsupported string is ignored", "1500", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			_, err := shipState(root, root, ShipStateIn{
				Action: "history_record",
				Detail: map[string]any{
					"skill":           "ship",
					"outcome":         "success",
					"ts":              "2026-01-01T00:00:00Z",
					"duration_ms":     tt.duration,
					"steps":           []any{"a", 3, "b"},
					"guardrail_hits":  []any{},
					"deferred_issues": "not-an-array",
				},
			}, shipErrNow)
			if err != nil {
				t.Fatalf("history_record: %v", err)
			}

			line := shipErrReadFile(t, filepath.Join(historyDir(root), "runs.jsonl"))
			var rec history.RunRecord
			if err := json.Unmarshal(line, &rec); err != nil {
				t.Fatalf("decode %q: %v", line, err)
			}
			if rec.DurationMs != tt.want {
				t.Errorf("duration_ms = %d, want %d", rec.DurationMs, tt.want)
			}
			if want := []string{"a", "b"}; !reflect.DeepEqual(rec.Steps, want) {
				t.Errorf("steps = %#v, want %#v (non-strings dropped)", rec.Steps, want)
			}
			if rec.GuardrailHits != nil || rec.DeferredIssues != nil {
				t.Errorf("guardrail_hits = %#v, deferred_issues = %#v, want both empty", rec.GuardrailHits, rec.DeferredIssues)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// gc dry run
// ---------------------------------------------------------------------------

func TestShipState_GC_DryRun_TTLDaysTypes(t *testing.T) {
	tests := []struct {
		name string
		ttl  any
		want int
	}{
		{"int is honoured", 3, 3},
		{"float64 is honoured", float64(3), 3},
		{"string falls back to the default", "3", 7},
		{"bool falls back to the default", true, 7},
		{"nil falls back to the default", nil, 7},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			out, err := shipState(dir, dir, ShipStateIn{
				Action: "gc",
				Detail: map[string]any{"dryRun": true, "ttlDays": tt.ttl},
			}, shipErrNow)
			if err != nil {
				t.Fatalf("gc dry-run: %v", err)
			}
			m, _ := out.(map[string]any)
			if m["ttlDays"] != tt.want {
				t.Errorf("ttlDays = %v, want %d", m["ttlDays"], tt.want)
			}
		})
	}
}

func TestShipState_GC_DryRun_IgnoresNonStateEntriesAndKeepsLiveBranch(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	checkoutBranch(t, dir, "feat/live")

	runs := filepath.Join(dir, paths.DataDir, paths.RunsSubdir)
	liveFile := filepath.Join(runs, "ship-feat-live-20200101T000000Z.json")
	writeFile(t, liveFile, `{}`)
	setStateFileMtime(t, liveFile, 30*24*time.Hour)

	// None of these may show up in any bucket.
	if err := os.MkdirAll(filepath.Join(runs, "ship-a-dir-20200101T000000Z.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(runs, "notes.txt"), "x")
	writeFile(t, filepath.Join(runs, "stray.json"), `{}`)

	out, err := shipState(dir, dir, ShipStateIn{
		Action: "gc",
		Detail: map[string]any{"dryRun": true},
	}, fixedNow(time.Now()))
	if err != nil {
		t.Fatalf("gc dry-run: %v", err)
	}
	m, _ := out.(map[string]any)

	entries := func(prefix, bucket string) []any {
		t.Helper()
		b, _ := m[prefix].(map[string]any)
		list, _ := b[bucket].([]any)
		return list
	}
	keep := entries("ship", "wouldKeep")
	if len(keep) != 1 {
		t.Fatalf("ship.wouldKeep = %v, want exactly the live-branch file", keep)
	}
	entry, _ := keep[0].(map[string]any)
	if entry["file"] != filepath.Base(liveFile) || entry["reason"] != "branch-exists" {
		t.Errorf("ship.wouldKeep[0] = %v, want file %s with reason branch-exists", entry, filepath.Base(liveFile))
	}
	for _, prefix := range []string{"ship", "execute", "plan"} {
		if got := entries(prefix, "wouldDelete"); len(got) != 0 {
			t.Errorf("%s.wouldDelete = %v, want empty", prefix, got)
		}
	}
	if got := entries("execute", "wouldKeep"); len(got) != 0 {
		t.Errorf("execute.wouldKeep = %v, want empty", got)
	}
}

// ---------------------------------------------------------------------------
// Malformed data inside a readable state file is tolerated
// ---------------------------------------------------------------------------

func shipErrRewriteState(t *testing.T, path string, mutate func(data map[string]any)) {
	t.Helper()
	data := readStateData(t, path)
	mutate(data)
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("marshal state: %v", err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func shipErrStepNames(t *testing.T, path string) []string {
	t.Helper()
	var names []string
	steps, _ := readStateData(t, path)["steps"].([]any)
	for _, s := range steps {
		step, _ := s.(map[string]any)
		name, _ := step["name"].(string)
		names = append(names, name)
	}
	return names
}

func TestShipState_MalformedStateEntries_AreSkipped(t *testing.T) {
	dir := t.TempDir()
	path := shipStateInitFixture(t, dir, shipErrBranch)
	names := shipErrStepNames(t, path)
	shipErrRewriteState(t, path, func(data map[string]any) {
		steps, _ := data["steps"].([]any)
		data["steps"] = append([]any{"not-a-step", 7}, steps...)
		data["sideEffects"] = map[string]any{
			"bogus":  "not-an-entry",
			names[1]: map[string]any{"kind": "pr", "ref": "#7"},
		}
	})

	call := func(in ShipStateIn) (any, error) {
		in.Detail = shipErrWithBranch(in.Detail)
		return shipState(dir, dir, in, shipErrNow)
	}

	if _, err := call(ShipStateIn{Action: "begin-step", Step: names[0]}); err != nil {
		t.Fatalf("begin-step: %v", err)
	}
	out, err := call(ShipStateIn{Action: "complete-step", Step: names[0]})
	if err != nil {
		t.Fatalf("complete-step: %v", err)
	}
	narration, ok := out.(ShipStepNarrationOut)
	if !ok {
		t.Fatalf("complete-step output = %#v, want ShipStepNarrationOut", out)
	}
	if narration.Next == nil || narration.Next.ID != names[1] {
		t.Errorf("next after %s = %+v, want %s", names[0], narration.Next, names[1])
	}

	out, err = call(ShipStateIn{Action: "read"})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	data, _ := out.(map[string]any)
	briefing, ok := data["resumeBriefing"].(*ShipResumeBriefing)
	if !ok {
		t.Fatalf("resumeBriefing type = %T, want *ShipResumeBriefing", data["resumeBriefing"])
	}
	if want := []string{names[1] + " (pr): #7"}; !reflect.DeepEqual(briefing.SideEffects, want) {
		t.Errorf("side effects = %v, want %v", briefing.SideEffects, want)
	}

	_, err = call(ShipStateIn{Action: "cleanup"})
	text, _ := requireShipErr(t, err, shipErrData)
	if !strings.Contains(text, names[1]) {
		t.Errorf("cleanup error = %q, want it to name the pending step %q", text, names[1])
	}
}

func TestShipState_Fail_NonStringErrorDetail(t *testing.T) {
	dir := t.TempDir()
	path := shipStateInitFixture(t, dir, shipErrBranch)
	step := shipErrStepNames(t, path)[0]

	_, err := shipState(dir, dir, ShipStateIn{
		Action: "fail",
		Step:   step,
		Detail: shipErrWithBranch(map[string]any{"error": 42}),
	}, shipErrNow)
	if err != nil {
		t.Fatalf("fail: %v", err)
	}

	issues, _ := readStateData(t, path)["issues"].([]any)
	if len(issues) != 1 {
		t.Fatalf("issues = %v, want exactly one", issues)
	}
	issue, _ := issues[0].(map[string]any)
	if issue["detail"] != "42" {
		t.Errorf("issue detail = %v, want %q", issue["detail"], "42")
	}
}

func TestShipState_Read_ResumeBriefing_TimestampEdgeCases(t *testing.T) {
	tests := []struct {
		name            string
		step            map[string]any
		wantStepSeconds int
		wantIdleSeconds int
		wantStepKnown   bool
		wantIdleKnown   bool
	}{
		{
			name: "in progress without startedAt",
			step: map[string]any{"status": "in_progress"},
		},
		{
			name: "unparsable startedAt",
			step: map[string]any{"status": "in_progress", "startedAt": "yesterday"},
		},
		{
			name: "failed step idles from completedAt",
			step: map[string]any{
				"status":      "failed",
				"startedAt":   "2026-01-01T10:00:00Z",
				"completedAt": "2026-01-01T10:05:00Z",
			},
			wantStepSeconds: 5 * 60,
			wantIdleSeconds: 13*3600 + 55*60,
			wantStepKnown:   true,
			wantIdleKnown:   true,
		},
		{
			name:          "startedAt after now clamps to zero",
			step:          map[string]any{"status": "in_progress", "startedAt": "2026-01-03T00:00:00Z"},
			wantStepKnown: true,
			wantIdleKnown: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := shipStateInitFixture(t, dir, shipErrBranch)
			first := shipErrStepNames(t, path)[0]
			shipErrRewriteState(t, path, func(data map[string]any) {
				steps, _ := data["steps"].([]any)
				entry := map[string]any{"name": first}
				for k, v := range tt.step {
					entry[k] = v
				}
				steps[0] = entry
			})

			out, err := shipState(dir, dir, ShipStateIn{Action: "read", Detail: shipErrWithBranch(nil)}, shipErrNow)
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			data, _ := out.(map[string]any)
			briefing, ok := data["resumeBriefing"].(*ShipResumeBriefing)
			if !ok {
				t.Fatalf("resumeBriefing type = %T, want *ShipResumeBriefing", data["resumeBriefing"])
			}
			timing := briefing.Timing
			if timing == nil {
				t.Fatal("resumeBriefing has no timing")
			}
			if timing.StepSeconds != tt.wantStepSeconds {
				t.Errorf("StepSeconds = %d, want %d", timing.StepSeconds, tt.wantStepSeconds)
			}
			if timing.IdleSeconds != tt.wantIdleSeconds {
				t.Errorf("IdleSeconds = %d, want %d", timing.IdleSeconds, tt.wantIdleSeconds)
			}
			if got := strings.Contains(timing.Human, "step "); got != tt.wantStepKnown {
				t.Errorf("step duration reported = %v, want %v (human = %q)", got, tt.wantStepKnown, timing.Human)
			}
			if got := strings.Contains(timing.Human, "idle "); got != tt.wantIdleKnown {
				t.Errorf("idle time reported = %v, want %v (human = %q)", got, tt.wantIdleKnown, timing.Human)
			}
		})
	}
}

func TestShipState_StepActions_AttachEtaFromTimingHistory(t *testing.T) {
	tests := []struct {
		action      string
		wantNextIdx int
		wantEta     int
	}{
		{"start", 0, 90},
		{"begin-step", 0, 90},
		{"complete-step", 1, 120},
	}
	for _, tt := range tests {
		t.Run(tt.action, func(t *testing.T) {
			dir := t.TempDir()
			path := shipStateInitFixture(t, dir, shipErrBranch)
			names := shipErrStepNames(t, path)
			store := pipeline.NewTimingsStore(dir)
			for i, d := range []time.Duration{90 * time.Second, 120 * time.Second} {
				if err := store.Record("ship:"+names[i], d); err != nil {
					t.Fatalf("seed timing history: %v", err)
				}
			}

			out, err := shipState(dir, dir, ShipStateIn{
				Action: tt.action,
				Step:   names[0],
				Detail: shipErrWithBranch(nil),
			}, shipErrNow)
			if err != nil {
				t.Fatalf("%s: %v", tt.action, err)
			}
			narration, ok := out.(ShipStepNarrationOut)
			if !ok {
				t.Fatalf("output = %#v, want ShipStepNarrationOut", out)
			}
			next := narration.Next
			if next == nil || next.ID != names[tt.wantNextIdx] {
				t.Fatalf("next = %+v, want step %s", next, names[tt.wantNextIdx])
			}
			if next.EtaSeconds != tt.wantEta || next.EtaBasis == "" {
				t.Errorf("next ETA = %ds (basis %q), want %ds with a basis", next.EtaSeconds, next.EtaBasis, tt.wantEta)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// RegisterShipStateTools: root resolution inside the registered handler
// ---------------------------------------------------------------------------

// callRegisteredShipState calls the registered tool over an in-memory MCP session.
func callRegisteredShipState(t *testing.T, args map[string]any) (*mcp.CallToolResult, string) {
	t.Helper()
	s := mcpserver.New("test", "0.0.0-test")
	RegisterShipStateTools(s)

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

	res, err := c.CallTool(ctx, &mcp.CallToolParams{Name: "ship_state", Arguments: args})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if len(res.Content) == 0 {
		t.Fatal("CallTool: no content")
	}
	text, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("content[0] is %T, want *mcp.TextContent", res.Content[0])
	}
	head, _, _ := strings.Cut(text.Text, "\n")
	return res, head
}

func TestRegisterShipStateTools_OutsideGitRepo(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(dir))
	t.Chdir(dir)

	res, head := callRegisteredShipState(t, map[string]any{"action": "read"})
	if !res.IsError {
		t.Error("IsError = false, want true when no git worktree contains the cwd")
	}
	if want := "# ship_state — error (infra)"; head != want {
		t.Errorf("head line = %q, want %q", head, want)
	}
}

func TestRegisterShipStateTools_ResolvesWorkDir(t *testing.T) {
	// In .git, worktree list works but rev-parse --show-toplevel fails, forcing the workDir fallback.
	tests := []struct {
		name string
		cwd  string
	}{
		{"cwd is the repo root", ""},
		{"cwd is the .git directory", ".git"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			initGitFixture(t, dir)
			gitCommit(t, dir, "initial")
			checkoutBranch(t, dir, "feat/reg")
			t.Chdir(filepath.Join(dir, tt.cwd))

			res, head := callRegisteredShipState(t, map[string]any{"action": "init"})
			if res.IsError {
				t.Fatalf("IsError = true, head line %q", head)
			}
			if want := "# ship_state — ok"; head != want {
				t.Errorf("head line = %q, want %q", head, want)
			}

			matches, err := filepath.Glob(filepath.Join(dir, paths.DataDir, paths.RunsSubdir, "ship-feat-reg-*.json"))
			if err != nil || len(matches) != 1 {
				t.Fatalf("state files = %v (err %v), want exactly one", matches, err)
			}
			data := readStateData(t, matches[0])
			wantDir, err := filepath.EvalSymlinks(dir)
			if err != nil {
				t.Fatal(err)
			}
			if data["worktree"] != wantDir {
				t.Errorf("worktree = %v, want %s", data["worktree"], wantDir)
			}
			if data["branch"] != "feat/reg" {
				t.Errorf("branch = %v, want feat/reg", data["branch"])
			}
		})
	}
}
