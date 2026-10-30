package tools

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
)

// detailJSON returns the JSON text of v.
func detailJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// detailQuote returns s as a JSON string literal.
func detailQuote(t *testing.T, s string) string {
	t.Helper()
	return detailJSON(t, s)
}

// detailExecData returns an execute state on branch with one completed wave
// whose only task "4" failed with taskError, so the run has a step with wave
// detail and one issue that carries taskError.
func detailExecData(branch, taskError string) map[string]any {
	d := dashJoinExecData(branch, "2026-10-07T08:01:00Z", "abc1234")
	d["waves"].([]any)[0].(map[string]any)["tasks"].([]any)[0].(map[string]any)["error"] = taskError
	return d
}

// detailWantRunMiss fails the test unless out is the miss result for id: not
// found, the id echoed, and both lists empty and non-nil in the value and in
// the JSON text.
func detailWantRunMiss(t *testing.T, out DashboardRunDetailOut, id string) {
	t.Helper()
	want := DashboardRunDetailOut{ID: id, Steps: []DashboardStep{}, Issues: []DashboardIssue{}}
	if !reflect.DeepEqual(out, want) {
		t.Errorf("out = %#v, want %#v", out, want)
	}
	wantJSON := fmt.Sprintf(`{"found":false,"id":%s,"steps":[],"issues":[]}`, detailQuote(t, id))
	if got := detailJSON(t, out); got != wantJSON {
		t.Errorf("JSON = %s, want %s", got, wantJSON)
	}
}

// detailWantSessionMiss fails the test unless out is the miss result for id:
// not found, the id echoed, and both lists empty and non-nil in the value and
// in the JSON text.
func detailWantSessionMiss(t *testing.T, out DashboardSessionDetailOut, id string) {
	t.Helper()
	want := DashboardSessionDetailOut{ID: id, Timeline: []DashboardEvent{}, CommandGroups: []DashboardCommandGroup{}}
	if !reflect.DeepEqual(out, want) {
		t.Errorf("out = %#v, want %#v", out, want)
	}
	wantJSON := fmt.Sprintf(`{"found":false,"id":%s,"timeline":[],"commandGroups":[]}`, detailQuote(t, id))
	if got := detailJSON(t, out); got != wantJSON {
		t.Errorf("JSON = %s, want %s", got, wantJSON)
	}
}

// detailSessionRepo writes the CLI evidence of root: a session s1 with 40 git
// commands and then 20 go commands, one a minute, the newest one minute
// before dashNow. It writes a session "prompt-only" with one prompt too.
func detailSessionRepo(t *testing.T) string {
	t.Helper()
	root := dashRoot(t)
	lines := make([]string, 60)
	for i := range lines {
		cmd := fmt.Sprintf("git log -%d", i)
		if i >= 40 {
			cmd = fmt.Sprintf("go test -count=%d", i)
		}
		ts := dashNow.Add(-time.Duration(60-i) * time.Minute).Format(time.RFC3339)
		lines[i] = fmt.Sprintf(`{"ts":"%s","branch":"feat/x","command":%s,"sessionId":"s1"}`, ts, detailQuote(t, cmd))
	}
	dashWriteJSONL(t, cliEvidencePath(root), lines...)
	dashWriteJSONL(t, userInputPath(root),
		`{"ts":"2026-10-07T09:10:00Z","text":"hello","sessionId":"prompt-only","kind":"prompt"}`)
	return root
}

// TestDashboardDetail_RunFound checks the id of a shown row: found true, the
// id, every step with its detail, and every issue, equal to the row of the
// snapshot of the same repo.
func TestDashboardDetail_RunFound(t *testing.T) {
	root := dashRoot(t)
	dashWriteState(t, root, archExecFile, detailExecData("feat/x", "go vet failed"), dashJoinFresh)
	row := dashOne(t, root)

	out, err := DashboardRunDetail(root, archExecID, dashNow)
	if err != nil {
		t.Fatalf("DashboardRunDetail: %v", err)
	}
	if !out.Found || out.ID != archExecID {
		t.Errorf("found, id = %v, %q; want true, %q", out.Found, out.ID, archExecID)
	}
	if !reflect.DeepEqual(out.Steps, row.Steps) {
		t.Errorf("steps = %+v, want %+v", out.Steps, row.Steps)
	}
	if !reflect.DeepEqual(out.Issues, row.Issues) {
		t.Errorf("issues = %+v, want %+v", out.Issues, row.Issues)
	}

	var waves *DashboardStepDetail
	for _, s := range out.Steps {
		if s.Detail != nil && s.Detail.Kind == dashboardKindWaves {
			waves = s.Detail
		}
	}
	if waves == nil || len(waves.Waves) != 1 || len(waves.Waves[0].Tasks) != 1 || waves.Waves[0].Tasks[0].ID != "4" {
		t.Fatalf("steps = %+v, want a step with a waves detail of task 4", out.Steps)
	}
	if len(out.Issues) != 1 || !strings.Contains(out.Issues[0].Text, "go vet failed") {
		t.Errorf("issues = %+v, want one issue with the task error", out.Issues)
	}

	got := detailJSON(t, out)
	if prefix := `{"found":true,"id":"` + archExecID + `","steps":[{`; !strings.HasPrefix(got, prefix) {
		t.Errorf("JSON = %s, want prefix %s", got, prefix)
	}
	if !strings.Contains(got, `"detail":{"kind":"waves"`) || !strings.Contains(got, `"issues":[{"source":`) {
		t.Errorf("JSON = %s, want the step detail and the issue list", got)
	}
}

// TestDashboardDetail_RunFoundNoIssue checks a shown row with no issue: found
// true and "issues":[] in the JSON text, never null.
func TestDashboardDetail_RunFoundNoIssue(t *testing.T) {
	root := archShipFixture(t, PipelineCompleted, dashJoinFresh)
	row := dashJoinFind(t, dashCollect(t, root).Pipelines, archShipID)
	if len(row.Issues) != 0 {
		t.Fatalf("fixture issues = %+v, want none", row.Issues)
	}

	out, err := DashboardRunDetail(root, archShipID, dashNow)
	if err != nil {
		t.Fatalf("DashboardRunDetail: %v", err)
	}
	if !out.Found || out.Issues == nil || len(out.Issues) != 0 {
		t.Errorf("found, issues = %v, %#v; want true, an empty non-nil slice", out.Found, out.Issues)
	}
	if got := detailJSON(t, out); !strings.Contains(got, `"issues":[]`) {
		t.Errorf("JSON = %s, want issues []", got)
	}
}

// TestDashboardDetail_RunMiss checks every id that is not a shown row: found
// false, the id echoed, and empty lists, with no error. An id with a path part
// gives the same miss, because the id is only compared with the row ids.
func TestDashboardDetail_RunMiss(t *testing.T) {
	root := dashRoot(t)
	dashWriteState(t, root, archExecFile, detailExecData("feat/x", "go vet failed"), dashJoinFresh)

	cases := []struct{ name, id string }{
		{"unknown id", "execute-feat-q-20261007T070000Z"},
		{"empty id", ""},
		{"parent folder", "../" + archExecID},
		{"path separator", "runs/" + archExecID},
		{"backslash", `runs\` + archExecID},
		{"trailing path part", archExecID + "/.."},
		{"absolute path", filepath.Join(root, ".sdlc-v2", "runs", archExecFile)},
		{"state file name", archExecFile},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, err := DashboardRunDetail(root, c.id, dashNow)
			if err != nil {
				t.Fatalf("DashboardRunDetail: %v", err)
			}
			detailWantRunMiss(t, out, c.id)
		})
	}
}

// TestDashboardDetail_RunMissInEmptyRepo checks a repo with no run: any id
// gives the miss result with no error.
func TestDashboardDetail_RunMissInEmptyRepo(t *testing.T) {
	root := dashRoot(t)
	out, err := DashboardRunDetail(root, archShipID, dashNow)
	if err != nil {
		t.Fatalf("DashboardRunDetail: %v", err)
	}
	detailWantRunMiss(t, out, archShipID)
}

// TestDashboardDetail_RunInShipBlock checks a ship with a nested execute run
// and a nested review run: the ship row is found, the unrelated row is found,
// and both nested runs give found false because the join drops them from the
// rows.
func TestDashboardDetail_RunInShipBlock(t *testing.T) {
	root := archShipFixture(t, PipelineCompleted, dashJoinFresh)
	rows := dashCollect(t, root).Pipelines

	for _, id := range []string{archShipID, archOtherID} {
		want := dashJoinFind(t, rows, id)
		out, err := DashboardRunDetail(root, id, dashNow)
		if err != nil {
			t.Fatalf("DashboardRunDetail(%q): %v", id, err)
		}
		if !out.Found || out.ID != id || len(out.Steps) != len(want.Steps) {
			t.Errorf("%s: found, id, steps = %v, %q, %d; want true, %q, %d", id, out.Found, out.ID, len(out.Steps), id, len(want.Steps))
		}
		if !reflect.DeepEqual(out.Steps, want.Steps) {
			t.Errorf("%s: steps = %+v, want %+v", id, out.Steps, want.Steps)
		}
	}

	for _, id := range []string{archExecID, archReview} {
		out, err := DashboardRunDetail(root, id, dashNow)
		if err != nil {
			t.Fatalf("DashboardRunDetail(%q): %v", id, err)
		}
		detailWantRunMiss(t, out, id)
	}
}

// TestDashboardDetail_RunWindow checks the history window: a completed or
// failed run shows while its updated time is at most dashboardPipelineWindow
// old and gives found false after that; a running run shows at any age.
func TestDashboardDetail_RunWindow(t *testing.T) {
	failed := map[string]any{
		"branch": "feat/x",
		"steps":  []any{map[string]any{"name": "execute", "status": StepFailed, "error": "boom"}},
	}
	cases := []struct {
		name   string
		data   map[string]any
		age    time.Duration
		status string
		found  bool
	}{
		{"completed inside the window", archShipData(PipelineCompleted), 3 * 24 * time.Hour, PipelineCompleted, true},
		{"completed at the window edge", archShipData(PipelineCompleted), dashboardPipelineWindow, PipelineCompleted, true},
		{"completed past the window", archShipData(PipelineCompleted), dashboardPipelineWindow + time.Second, PipelineCompleted, false},
		{"failed inside the window", failed, 3 * 24 * time.Hour, PipelineFailed, true},
		{"failed past the window", failed, 5 * 24 * time.Hour, PipelineFailed, false},
		{"running past the window", archShipData(""), 6 * 24 * time.Hour, PipelineStalled, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := dashRoot(t)
			dashWriteState(t, root, archShipFile, c.data, dashNow.Add(-c.age))

			rows := dashCollect(t, root).Pipelines
			if shown := len(rows) == 1; shown != c.found {
				t.Fatalf("rows = %+v, want shown = %v", rows, c.found)
			}
			if c.found && rows[0].Status != c.status {
				t.Fatalf("row status = %q, want %q", rows[0].Status, c.status)
			}

			out, err := DashboardRunDetail(root, archShipID, dashNow)
			if err != nil {
				t.Fatalf("DashboardRunDetail: %v", err)
			}
			if out.Found != c.found {
				t.Errorf("found = %v, want %v", out.Found, c.found)
			}
			if !c.found {
				detailWantRunMiss(t, out, archShipID)
			}
		})
	}
}

// detailCollectErrorCase is one repo that the collect cannot read.
type detailCollectErrorCase struct {
	name, root, cause string
	// domain is true when the error is a DomainError, else an InfraError.
	domain bool
	// is is the error that errors.Is finds in the chain of the error.
	is error
}

// detailCollectErrorCases returns a repo folder that is gone, a repo path that
// is a file, and a repo whose runs folder is a file.
func detailCollectErrorCases(t *testing.T) []detailCollectErrorCase {
	t.Helper()
	file := filepath.Join(t.TempDir(), "plain-file")
	writeFile(t, file, "x")
	runsFile := dashRoot(t)
	runs := filepath.Join(runsFile, paths.DataDir, paths.RunsSubdir)
	if err := os.RemoveAll(runs); err != nil {
		t.Fatal(err)
	}
	writeFile(t, runs, "x")
	return []detailCollectErrorCase{
		{name: "folder is gone", root: filepath.Join(t.TempDir(), "gone"), cause: "repo folder not readable", is: fs.ErrNotExist},
		{name: "path is a file", root: file, cause: "repo path is not a folder", domain: true, is: errDashboardRepoNotFolder},
		{name: "runs folder is a file", root: runsFile, cause: runs},
	}
}

// detailWantCollectError fails the test unless err is the error of c: the
// error class, an error that names the repo and the cause, the cause in the
// chain, and the suggestion for a repo that cannot be read.
func detailWantCollectError(t *testing.T, c detailCollectErrorCase, err error) {
	t.Helper()
	var suggestion string
	var cause error
	var domain *mcpserver.DomainError
	var infra *mcpserver.InfraError
	switch {
	case c.domain && errors.As(err, &domain):
		suggestion, cause = domain.Suggestion, domain.Cause
	case !c.domain && errors.As(err, &infra):
		suggestion, cause = infra.Suggestion, infra.Cause
	default:
		t.Fatalf("err = %v (%T), want a DomainError: %v", err, err, c.domain)
	}
	if !strings.Contains(err.Error(), c.root) || !strings.Contains(err.Error(), c.cause) {
		t.Errorf("err = %q, want it to name %q and %q", err, c.root, c.cause)
	}
	if cause == nil {
		t.Error("error has no cause")
	}
	if c.is != nil && !errors.Is(err, c.is) {
		t.Errorf("errors.Is(err, %v) = false, want true", c.is)
	}
	if suggestion != dashboardDetailRepoSuggestion {
		t.Errorf("suggestion = %q, want %q", suggestion, dashboardDetailRepoSuggestion)
	}
}

// TestDashboardDetail_RunCollectError checks a repo that the collect cannot
// read: the error of each cause, and the miss result.
func TestDashboardDetail_RunCollectError(t *testing.T) {
	for _, c := range detailCollectErrorCases(t) {
		t.Run(c.name, func(t *testing.T) {
			out, err := DashboardRunDetail(c.root, archShipID, dashNow)
			detailWantCollectError(t, c, err)
			detailWantRunMiss(t, out, archShipID)
		})
	}
}

// TestDashboardDetail_RunPicksRequestedRepo checks the repo choice: two repos
// hold a run of the same id with different data, and each repo also holds a
// run of its own. The result comes from the requested repo only.
func TestDashboardDetail_RunPicksRequestedRepo(t *testing.T) {
	const aOnly, bOnly = "execute-feat-a-20261007T090000Z", "execute-feat-b-20261007T090100Z"
	rootA, rootB := dashRoot(t), dashRoot(t)
	dashWriteState(t, rootA, archExecFile, detailExecData("feat/alpha", "alpha failure"), dashJoinFresh)
	dashWriteState(t, rootB, archExecFile, detailExecData("feat/beta", "beta failure"), dashJoinFresh)
	dashWriteState(t, rootA, aOnly+".json", detailExecData("feat/a", "a-only failure"), dashJoinFresh)
	dashWriteState(t, rootB, bOnly+".json", detailExecData("feat/b", "b-only failure"), dashJoinFresh)

	cases := []struct {
		name, root, id, issue string
	}{
		{"repo A, shared id", rootA, archExecID, "alpha failure"},
		{"repo B, shared id", rootB, archExecID, "beta failure"},
		{"repo A, own id", rootA, aOnly, "a-only failure"},
		{"repo B, own id", rootB, bOnly, "b-only failure"},
		{"repo A, id of repo B", rootA, bOnly, ""},
		{"repo B, id of repo A", rootB, aOnly, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, err := DashboardRunDetail(c.root, c.id, dashNow)
			if err != nil {
				t.Fatalf("DashboardRunDetail: %v", err)
			}
			if c.issue == "" {
				detailWantRunMiss(t, out, c.id)
				return
			}
			if !out.Found || len(out.Issues) != 1 || !strings.Contains(out.Issues[0].Text, c.issue) {
				t.Errorf("out = %+v, want found with an issue of %q", out, c.issue)
			}
		})
	}
}

// TestDashboardDetail_SessionFound checks a session of 60 commands: the
// newest 50 events newest first, all command groups counted over the 60
// commands, and the same values as the session of the snapshot of the repo.
func TestDashboardDetail_SessionFound(t *testing.T) {
	root := detailSessionRepo(t)
	var want DashboardSession
	for _, s := range dashCollect(t, root).Sessions {
		if s.ID == "s1" {
			want = s
		}
	}

	out, err := DashboardSessionDetail(root, "s1", dashNow)
	if err != nil {
		t.Fatalf("DashboardSessionDetail: %v", err)
	}
	if !out.Found || out.ID != "s1" {
		t.Errorf("found, id = %v, %q; want true, s1", out.Found, out.ID)
	}
	if len(out.Timeline) != dashboardSessionTimelineMax {
		t.Fatalf("timeline = %d events, want %d", len(out.Timeline), dashboardSessionTimelineMax)
	}
	newest := DashboardEvent{At: dashNow.Add(-time.Minute).Format(time.RFC3339), Kind: "command", Text: "go test -count=59"}
	oldest := DashboardEvent{At: dashNow.Add(-50 * time.Minute).Format(time.RFC3339), Kind: "command", Text: "git log -10"}
	if out.Timeline[0] != newest || out.Timeline[len(out.Timeline)-1] != oldest {
		t.Errorf("timeline ends = %+v and %+v, want %+v and %+v", out.Timeline[0], out.Timeline[len(out.Timeline)-1], newest, oldest)
	}
	if !reflect.DeepEqual(out.Timeline, want.Timeline) || !reflect.DeepEqual(out.CommandGroups, want.CommandGroups) {
		t.Errorf("lists differ from the snapshot session: got %+v and %+v, want %+v and %+v", out.Timeline, out.CommandGroups, want.Timeline, want.CommandGroups)
	}

	if len(out.CommandGroups) != 2 {
		t.Fatalf("groups = %+v, want 2", out.CommandGroups)
	}
	git, goG := out.CommandGroups[0], out.CommandGroups[1]
	if git.Label != "git" || git.Count != 40 || git.Share != 0.67 || !git.Majority {
		t.Errorf("git group = %+v, want git 40 0.67 majority", git)
	}
	if goG.Label != "go" || goG.Count != 20 || goG.Share != 0.33 || goG.Majority || goG.LastAt != newest.At {
		t.Errorf("go group = %+v, want go 20 0.33 no majority, last at %s", goG, newest.At)
	}

	got := detailJSON(t, out)
	if prefix := `{"found":true,"id":"s1","timeline":[{"at":"` + newest.At + `","kind":"command","text":"go test -count=59"}`; !strings.HasPrefix(got, prefix) {
		t.Errorf("JSON = %s, want prefix %s", got, prefix)
	}
	if !strings.Contains(got, `"commandGroups":[{"label":"git","programs":["git"],"count":40,"share":0.67,"majority":true,`) {
		t.Errorf("JSON = %s, want the git command group", got)
	}
}

// TestDashboardDetail_SessionNoCommand checks a session with one prompt and no
// command: found true, the one event, and "commandGroups":[] in the JSON text,
// never null.
func TestDashboardDetail_SessionNoCommand(t *testing.T) {
	root := detailSessionRepo(t)
	out, err := DashboardSessionDetail(root, "prompt-only", dashNow)
	if err != nil {
		t.Fatalf("DashboardSessionDetail: %v", err)
	}
	want := DashboardSessionDetailOut{
		Found: true, ID: "prompt-only",
		Timeline:      []DashboardEvent{{At: "2026-10-07T09:10:00Z", Kind: "prompt", Text: "hello"}},
		CommandGroups: []DashboardCommandGroup{},
	}
	if !reflect.DeepEqual(out, want) {
		t.Errorf("out = %#v, want %#v", out, want)
	}
	wantJSON := `{"found":true,"id":"prompt-only","timeline":[{"at":"2026-10-07T09:10:00Z","kind":"prompt","text":"hello"}],"commandGroups":[]}`
	if got := detailJSON(t, out); got != wantJSON {
		t.Errorf("JSON = %s, want %s", got, wantJSON)
	}
}

// TestDashboardDetail_SessionMiss checks every id that is not a session of the
// repo: found false, the id echoed, and empty lists, with no error. A repo with
// no session gives the same miss. An id with a path part is only compared.
func TestDashboardDetail_SessionMiss(t *testing.T) {
	withSessions := detailSessionRepo(t)
	noSessions := dashRoot(t)
	cases := []struct{ name, root, id string }{
		{"unknown id", withSessions, "s-unknown"},
		{"empty id", withSessions, ""},
		{"parent folder", withSessions, "../s1"},
		{"path separator", withSessions, "x/s1"},
		{"backslash", withSessions, `x\s1`},
		{"pipeline id", withSessions, archShipID},
		{"repo with no session", noSessions, "s1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, err := DashboardSessionDetail(c.root, c.id, dashNow)
			if err != nil {
				t.Fatalf("DashboardSessionDetail: %v", err)
			}
			detailWantSessionMiss(t, out, c.id)
		})
	}
}

// TestDashboardDetail_SessionCollectError checks a repo that the collect
// cannot read: the error of each cause, and the miss result.
func TestDashboardDetail_SessionCollectError(t *testing.T) {
	for _, c := range detailCollectErrorCases(t) {
		t.Run(c.name, func(t *testing.T) {
			out, err := DashboardSessionDetail(c.root, "s1", dashNow)
			detailWantCollectError(t, c, err)
			detailWantSessionMiss(t, out, "s1")
		})
	}
}

// TestDashboardDetail_SessionEvidenceUnreadable checks an evidence file that
// cannot be read: an InfraError that names the file, not a miss, for a session
// that is in the other files and for a session that is in no file.
func TestDashboardDetail_SessionEvidenceUnreadable(t *testing.T) {
	for _, id := range []string{"prompt-only", "s1"} {
		t.Run(id, func(t *testing.T) {
			root := detailSessionRepo(t)
			cli := cliEvidencePath(root)
			if err := os.Remove(cli); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(cli, 0o755); err != nil {
				t.Fatal(err)
			}
			out, err := DashboardSessionDetail(root, id, dashNow)
			var infra *mcpserver.InfraError
			if !errors.As(err, &infra) {
				t.Fatalf("err = %v (%T), want *mcpserver.InfraError", err, err)
			}
			if !strings.Contains(err.Error(), cli) || infra.Cause == nil {
				t.Errorf("err = %q, cause = %v; want it to name %s and carry the cause", err, infra.Cause, cli)
			}
			if infra.Suggestion != dashboardDetailEvidenceSuggestion {
				t.Errorf("suggestion = %q, want %q", infra.Suggestion, dashboardDetailEvidenceSuggestion)
			}
			detailWantSessionMiss(t, out, id)
		})
	}
}

// TestDashboardDetail_SessionPicksRequestedRepo checks the repo choice: two
// repos hold a session of the same id with different commands, and each repo
// also holds a session of its own. The result comes from the requested repo
// only.
func TestDashboardDetail_SessionPicksRequestedRepo(t *testing.T) {
	rootA, rootB := dashRoot(t), dashRoot(t)
	dashWriteJSONL(t, cliEvidencePath(rootA),
		`{"ts":"2026-10-07T09:00:00Z","branch":"feat/a","command":"make alpha","sessionId":"shared"}`,
		`{"ts":"2026-10-07T09:01:00Z","branch":"feat/a","command":"make alpha-own","sessionId":"a-only"}`)
	dashWriteJSONL(t, cliEvidencePath(rootB),
		`{"ts":"2026-10-07T09:00:00Z","branch":"feat/b","command":"cargo beta","sessionId":"shared"}`,
		`{"ts":"2026-10-07T09:01:00Z","branch":"feat/b","command":"cargo beta-own","sessionId":"b-only"}`)

	cases := []struct {
		name, root, id, text, group string
	}{
		{"repo A, shared id", rootA, "shared", "make alpha", "make"},
		{"repo B, shared id", rootB, "shared", "cargo beta", "cargo"},
		{"repo A, own id", rootA, "a-only", "make alpha-own", "make"},
		{"repo B, own id", rootB, "b-only", "cargo beta-own", "cargo"},
		{"repo A, id of repo B", rootA, "b-only", "", ""},
		{"repo B, id of repo A", rootB, "a-only", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, err := DashboardSessionDetail(c.root, c.id, dashNow)
			if err != nil {
				t.Fatalf("DashboardSessionDetail: %v", err)
			}
			if c.text == "" {
				detailWantSessionMiss(t, out, c.id)
				return
			}
			if !out.Found || len(out.Timeline) != 1 || out.Timeline[0].Text != c.text {
				t.Errorf("timeline = %+v, want one event %q", out.Timeline, c.text)
			}
			if len(out.CommandGroups) != 1 || out.CommandGroups[0].Label != c.group {
				t.Errorf("groups = %+v, want one group %q", out.CommandGroups, c.group)
			}
		})
	}
}

// TestDashboardDetail_SessionResultIsACopy checks that a change of a returned
// list does not reach the next call: the sessions come from the parse cache,
// which shares its lists between calls.
func TestDashboardDetail_SessionResultIsACopy(t *testing.T) {
	root := detailSessionRepo(t)
	first, err := DashboardSessionDetail(root, "s1", dashNow)
	if err != nil {
		t.Fatalf("DashboardSessionDetail: %v", err)
	}
	wantText, wantLabel := first.Timeline[0].Text, first.CommandGroups[0].Label
	first.Timeline[0].Text = "changed"
	first.CommandGroups[0].Label = "changed"

	second, err := DashboardSessionDetail(root, "s1", dashNow)
	if err != nil {
		t.Fatalf("DashboardSessionDetail: %v", err)
	}
	if second.Timeline[0].Text != wantText || second.CommandGroups[0].Label != wantLabel {
		t.Errorf("second call = %q and %q, want %q and %q", second.Timeline[0].Text, second.CommandGroups[0].Label, wantText, wantLabel)
	}
}
