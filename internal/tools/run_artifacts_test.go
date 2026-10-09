package tools

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"

	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
	"github.com/rnagrodzki/sdlc-plugin/internal/state"
)

// raTestStartedAt is the RFC3339 startedAt value the tests put in state data.
const raTestStartedAt = "2026-10-08T12:00:00Z"

// raTestRunID is the run id that execRunID derives from raTestStartedAt.
const raTestRunID = "20261008T120000"

// raRunsDir returns the runs/ directory under the data directory of root.
func raRunsDir(root string) string {
	return filepath.Join(root, paths.DataDir, paths.RunsSubdir)
}

// raReportsDir returns the reports/ directory under the data directory of root.
func raReportsDir(root string) string {
	return filepath.Join(root, paths.DataDir, paths.ReportsSubdir)
}

// raTouch creates an empty file, creating parent directories first.
func raTouch(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// raMkdir creates a directory with one file inside it.
func raMkdir(t *testing.T, dir string) {
	t.Helper()
	raTouch(t, filepath.Join(dir, "file.json"))
}

// raState writes a state file of the given kind and returns the loaded state.
func raState(t *testing.T, root, prefix, branch string, data map[string]any) *state.State {
	t.Helper()
	st, err := state.Init(root, prefix, branch, "")
	if err != nil {
		t.Fatalf("state.Init(%s): %v", prefix, err)
	}
	for k, v := range data {
		st.Data[k] = v
	}
	if err := state.Write(st); err != nil {
		t.Fatalf("state.Write(%s): %v", prefix, err)
	}
	return st
}

// TestExecRunID checks that execRunID keeps only digits and 'T' from startedAt
// and returns "" when startedAt is absent, empty, not a string or has no digits.
func TestExecRunID(t *testing.T) {
	tests := []struct {
		name string
		data map[string]any
		want string
	}{
		{"RFC3339", map[string]any{"startedAt": raTestStartedAt}, raTestRunID},
		{"fractional seconds", map[string]any{"startedAt": "2026-10-08T12:00:00.123Z"}, "20261008T120000123"},
		{"absent", map[string]any{}, ""},
		{"nil map", nil, ""},
		{"empty string", map[string]any{"startedAt": ""}, ""},
		{"not a string", map[string]any{"startedAt": 20261008}, ""},
		{"no digits", map[string]any{"startedAt": "abc"}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := execRunID(tt.data); got != tt.want {
				t.Errorf("execRunID(%v) = %q, want %q", tt.data, got, tt.want)
			}
		})
	}
}

// TestExecDeriveRunID_UsesExecRunID checks that execDeriveRunID returns the
// execRunID result when startedAt is set and "wave-<n>" when it is not.
func TestExecDeriveRunID_UsesExecRunID(t *testing.T) {
	if got := execDeriveRunID(map[string]any{"startedAt": raTestStartedAt}, 3); got != raTestRunID {
		t.Errorf("with startedAt: got %q, want %q", got, raTestRunID)
	}
	if got := execDeriveRunID(map[string]any{}, 3); got != "wave-3" {
		t.Errorf("without startedAt: got %q, want %q", got, "wave-3")
	}
	// A startedAt with no digit and no T strips to "": the wave fallback applies.
	if got := execDeriveRunID(map[string]any{"startedAt": "abc"}, 3); got != "wave-3" {
		t.Errorf("with startedAt %q: got %q, want %q", "abc", got, "wave-3")
	}
}

// TestExecRunID_IsTheOnlyRuleCopy pins the drift guard: one non-test source
// line applies the run id rule, and it is the one in execRunID.
func TestExecRunID_IsTheOnlyRuleCopy(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	var hits []string
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(src), "\n") {
			if strings.Contains(line, "execNonDigitTRE.ReplaceAllString") {
				hits = append(hits, fmt.Sprintf("%s:%d", f, i+1))
				if f != "run_artifacts.go" {
					t.Errorf("%s line %d applies the run id rule; call execRunID instead", f, i+1)
				}
			}
		}
	}
	if len(hits) != 1 {
		t.Errorf("found %d copies of the run id rule (%v), want exactly 1 in run_artifacts.go", len(hits), hits)
	}
}

// TestBareRunName checks that bareRunName rejects empty names, dot names and
// names with a path separator, and accepts plain names.
func TestBareRunName(t *testing.T) {
	bad := []string{"", ".", "..", "a/b", `a\b`, "/abs", "../x", "x/..", "x/", `x\`}
	for _, id := range bad {
		if err := bareRunName(id); err == nil {
			t.Errorf("bareRunName(%q) = nil, want error", id)
		}
	}
	good := []string{raTestRunID, "review-20261008T120000Z", "plan-feat-x-20261008T120000Z", "a.b", "..a"}
	for _, id := range good {
		if err := bareRunName(id); err != nil {
			t.Errorf("bareRunName(%q) = %v, want nil", id, err)
		}
	}
}

// TestReviewLedgerDir checks that ReviewLedgerDir maps a review run name to
// runs/ledger/<name>/ and returns an error and no path for any other name.
func TestReviewLedgerDir(t *testing.T) {
	root := t.TempDir()
	ledgerRoot := filepath.Join(raRunsDir(root), "ledger")

	got, err := ReviewLedgerDir(root, "review-20261008T120000Z")
	if err != nil {
		t.Fatalf("ReviewLedgerDir: %v", err)
	}
	want := filepath.Join(ledgerRoot, "review-20261008T120000Z")
	if got != want {
		t.Errorf("ReviewLedgerDir = %q, want %q", got, want)
	}

	bad := []string{
		"", ".", "..",
		raTestRunID,               // execute run id: no review- prefix
		"execute-x",               // wrong prefix
		"Review-20261008T120000Z", // prefix is case-sensitive
		"review-",                 // prefix only
		"review-a/b", `review-a\b`,
		"../review-x",
		"ledger",
	}
	for _, name := range bad {
		got, err := ReviewLedgerDir(root, name)
		if err == nil {
			t.Errorf("ReviewLedgerDir(%q) = %q, want error", name, got)
		}
		if got != "" {
			t.Errorf("ReviewLedgerDir(%q) returned path %q with error, want none", name, got)
		}
	}
}

// TestReportOwner checks that ReportOwner returns the run id for the two
// report file name forms and rejects every other file name.
func TestReportOwner(t *testing.T) {
	tests := []struct {
		name   string
		wantID string
		wantOK bool
	}{
		{"20261008T120000-report.md", raTestRunID, true},
		{"20261008T120000-report.json", raTestRunID, true},
		{"ship-20261008T120000-report.md", raTestRunID, true},
		{"ship-20261008T120000-report.json", raTestRunID, true},
		{"-report.md", "", false},
		{"ship--report.md", "", false},
		{"ship-wave-0-report.md", "", false},
		{"wave-0-report.md", "", false},
		{"20261008T120000-report.txt", "", false},
		{"20261008T120000-report.md.tmp-123", "", false},
		{"20261008T120000-report", "", false},
		{"20261008T120000.md", "", false},
		{"review-20261008T120000Z-report.md", "", false},
		{"../20261008T120000-report.md", "", false},
		{"sub/20261008T120000-report.md", "", false},
		{"README.md", "", false},
		{"", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id, ok := ReportOwner(tt.name)
			if id != tt.wantID || ok != tt.wantOK {
				t.Errorf("ReportOwner(%q) = (%q, %v), want (%q, %v)", tt.name, id, ok, tt.wantID, tt.wantOK)
			}
		})
	}
}

// TestResolveRunArtifacts_Execute checks that an execute run claims its
// ledger folder, its reports and its run directory, and no neighbor files.
func TestResolveRunArtifacts_Execute(t *testing.T) {
	root := t.TempDir()
	st := raState(t, root, "execute", "feat/x", map[string]any{"startedAt": raTestStartedAt})

	runDir := filepath.Join(raRunsDir(root), raTestRunID)
	ledger := filepath.Join(raRunsDir(root), "ledger", raTestRunID)
	raMkdir(t, runDir)
	raMkdir(t, ledger)
	raTouch(t, filepath.Join(raReportsDir(root), raTestRunID+"-report.md"))
	raTouch(t, filepath.Join(raReportsDir(root), raTestRunID+"-report.json"))
	// Neighbors that must not be claimed by this run.
	raTouch(t, filepath.Join(raReportsDir(root), "ship-"+raTestRunID+"-report.md"))
	raTouch(t, filepath.Join(raReportsDir(root), raTestRunID+"-report.md.tmp-1"))
	raTouch(t, filepath.Join(raReportsDir(root), "20261008T120001-report.md"))
	raMkdir(t, filepath.Join(raRunsDir(root), "20261008T120001"))

	got, err := ResolveRunArtifacts(root, st)
	if err != nil {
		t.Fatalf("ResolveRunArtifacts: %v", err)
	}
	want := RunArtifacts{
		StateFile: st.Path,
		Keep: []string{
			ledger,
			filepath.Join(raReportsDir(root), raTestRunID+"-report.md"),
			filepath.Join(raReportsDir(root), raTestRunID+"-report.json"),
		},
		Working: []string{runDir},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ResolveRunArtifacts =\n%+v\nwant\n%+v", got, want)
	}
}

// TestResolveRunArtifacts_ExecuteMissingArtifactsIsNotAnError checks that an
// execute run with no artifacts on disk returns only the state file.
func TestResolveRunArtifacts_ExecuteMissingArtifactsIsNotAnError(t *testing.T) {
	root := t.TempDir()
	st := raState(t, root, "execute", "feat/x", map[string]any{"startedAt": raTestStartedAt})

	got, err := ResolveRunArtifacts(root, st)
	if err != nil {
		t.Fatalf("ResolveRunArtifacts: %v", err)
	}
	want := RunArtifacts{StateFile: st.Path}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ResolveRunArtifacts = %+v, want only the state file %+v", got, want)
	}
}

// TestResolveRunArtifacts_ExecutePartialArtifacts checks that an execute run
// with one report on disk returns that report and no working path.
func TestResolveRunArtifacts_ExecutePartialArtifacts(t *testing.T) {
	root := t.TempDir()
	st := raState(t, root, "execute", "feat/x", map[string]any{"startedAt": raTestStartedAt})
	raTouch(t, filepath.Join(raReportsDir(root), raTestRunID+"-report.json"))

	got, err := ResolveRunArtifacts(root, st)
	if err != nil {
		t.Fatalf("ResolveRunArtifacts: %v", err)
	}
	wantKeep := []string{filepath.Join(raReportsDir(root), raTestRunID+"-report.json")}
	if !reflect.DeepEqual(got.Keep, wantKeep) || len(got.Working) != 0 {
		t.Errorf("got Keep=%v Working=%v, want Keep=%v and no Working", got.Keep, got.Working, wantKeep)
	}
}

// TestResolveRunArtifacts_Ship checks that a ship run claims only its own
// ship-<id> reports, not an execute report with the same id and not a run dir.
func TestResolveRunArtifacts_Ship(t *testing.T) {
	root := t.TempDir()
	st := raState(t, root, "ship", "feat/x", map[string]any{"startedAt": raTestStartedAt})

	raTouch(t, filepath.Join(raReportsDir(root), "ship-"+raTestRunID+"-report.md"))
	raTouch(t, filepath.Join(raReportsDir(root), "ship-"+raTestRunID+"-report.json"))
	// An execute report with the same id belongs to the execute run.
	raTouch(t, filepath.Join(raReportsDir(root), raTestRunID+"-report.md"))
	// A ship run has no per-run directory under runs/.
	raMkdir(t, filepath.Join(raRunsDir(root), raTestRunID))

	got, err := ResolveRunArtifacts(root, st)
	if err != nil {
		t.Fatalf("ResolveRunArtifacts: %v", err)
	}
	want := RunArtifacts{
		StateFile: st.Path,
		Keep: []string{
			filepath.Join(raReportsDir(root), "ship-"+raTestRunID+"-report.md"),
			filepath.Join(raReportsDir(root), "ship-"+raTestRunID+"-report.json"),
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ResolveRunArtifacts =\n%+v\nwant\n%+v", got, want)
	}
}

// TestResolveRunArtifacts_Plan checks that a plan run keeps its evidence
// brief.md and lists its evidence directory as working.
func TestResolveRunArtifacts_Plan(t *testing.T) {
	root := t.TempDir()
	st := raState(t, root, "plan", "feat/x", map[string]any{})
	evidence := state.EvidenceDir(root, state.RunID(st))
	if want := strings.TrimSuffix(st.Path, ".json") + ".evidence"; evidence != want {
		t.Fatalf("EvidenceDir = %q, want %q", evidence, want)
	}
	brief := filepath.Join(evidence, "brief.md")
	raTouch(t, brief)
	raTouch(t, filepath.Join(evidence, "explorer.json"))

	got, err := ResolveRunArtifacts(root, st)
	if err != nil {
		t.Fatalf("ResolveRunArtifacts: %v", err)
	}
	want := RunArtifacts{StateFile: st.Path, Keep: []string{brief}, Working: []string{evidence}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ResolveRunArtifacts =\n%+v\nwant\n%+v", got, want)
	}
}

// TestResolveRunArtifacts_PlanWithoutBrief checks that a plan run with no
// brief.md keeps nothing and still lists its evidence directory as working.
func TestResolveRunArtifacts_PlanWithoutBrief(t *testing.T) {
	root := t.TempDir()
	st := raState(t, root, "plan", "feat/x", map[string]any{})
	evidence := state.EvidenceDir(root, state.RunID(st))
	raTouch(t, filepath.Join(evidence, "explorer.json"))

	got, err := ResolveRunArtifacts(root, st)
	if err != nil {
		t.Fatalf("ResolveRunArtifacts: %v", err)
	}
	want := RunArtifacts{StateFile: st.Path, Working: []string{evidence}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ResolveRunArtifacts = %+v, want %+v", got, want)
	}
}

// TestResolveRunArtifacts_EmptyIDReturnsErrorAndNoPath checks that an execute
// or ship state with no usable startedAt returns an error and an empty result.
func TestResolveRunArtifacts_EmptyIDReturnsErrorAndNoPath(t *testing.T) {
	root := t.TempDir()
	// Data a wrong rule would turn into runs/ and runs/ledger/ themselves.
	raMkdir(t, filepath.Join(raRunsDir(root), "ledger", "other"))
	raTouch(t, filepath.Join(raReportsDir(root), "-report.md"))

	for _, prefix := range []string{"execute", "ship"} {
		for name, data := range map[string]map[string]any{
			"absent":    {},
			"empty":     {"startedAt": ""},
			"no digits": {"startedAt": "abc"},
		} {
			t.Run(prefix+"/"+name, func(t *testing.T) {
				st := &state.State{
					Path:   filepath.Join(raRunsDir(root), prefix+"-feat-x-20261008T120000Z.json"),
					Root:   root,
					Prefix: prefix,
					Data:   data,
				}
				got, err := ResolveRunArtifacts(root, st)
				if err == nil {
					t.Fatalf("ResolveRunArtifacts = %+v, want error", got)
				}
				if !reflect.DeepEqual(got, RunArtifacts{}) {
					t.Errorf("ResolveRunArtifacts returned %+v with an error, want no path", got)
				}
			})
		}
	}
}

// TestResolveRunArtifacts_PlanWithoutFileNameReturnsError checks that a plan
// state with no file path returns an error and an empty result.
func TestResolveRunArtifacts_PlanWithoutFileNameReturnsError(t *testing.T) {
	root := t.TempDir()
	st := &state.State{Root: root, Prefix: "plan", Data: map[string]any{}}

	got, err := ResolveRunArtifacts(root, st)
	if err == nil {
		t.Fatalf("ResolveRunArtifacts = %+v, want error", got)
	}
	if !reflect.DeepEqual(got, RunArtifacts{}) {
		t.Errorf("ResolveRunArtifacts returned %+v with an error, want no path", got)
	}
}

// TestResolveRunArtifacts_UnsupportedKindAndNil checks that a nil state and a
// state kind with no per-run artifacts both return an error.
func TestResolveRunArtifacts_UnsupportedKindAndNil(t *testing.T) {
	root := t.TempDir()
	if _, err := ResolveRunArtifacts(root, nil); err == nil {
		t.Error("nil state: want error")
	}
	st := &state.State{
		Path:   filepath.Join(raRunsDir(root), "commit-feat-x-20261008T120000Z.json"),
		Root:   root,
		Prefix: "commit",
		Data:   map[string]any{"startedAt": raTestStartedAt},
	}
	got, err := ResolveRunArtifacts(root, st)
	if err == nil {
		t.Errorf("commit state: ResolveRunArtifacts = %+v, want error", got)
	}
}

// TestResolveRunArtifacts_NeverReturnsRunsOrLedgerRoot checks the safety rule
// for archive and clear: no resolved path is runs/, runs/ledger/ or reports/.
func TestResolveRunArtifacts_NeverReturnsRunsOrLedgerRoot(t *testing.T) {
	root := t.TempDir()
	raMkdir(t, filepath.Join(raRunsDir(root), "ledger", raTestRunID))
	raMkdir(t, filepath.Join(raRunsDir(root), raTestRunID))
	raTouch(t, filepath.Join(raReportsDir(root), raTestRunID+"-report.md"))

	forbidden := map[string]bool{
		raRunsDir(root):                          true,
		filepath.Join(raRunsDir(root), "ledger"): true,
		raReportsDir(root):                       true,
	}
	for _, prefix := range []string{"execute", "ship", "plan"} {
		st := raState(t, root, prefix, "feat/"+prefix, map[string]any{"startedAt": raTestStartedAt})
		got, err := ResolveRunArtifacts(root, st)
		if err != nil {
			t.Fatalf("%s: ResolveRunArtifacts: %v", prefix, err)
		}
		all := append(append([]string{got.StateFile}, got.Keep...), got.Working...)
		for _, p := range all {
			if forbidden[p] {
				t.Errorf("%s: resolved forbidden root %q", prefix, p)
			}
		}
	}
}

// TestReportOwner_AcceptsEveryReportPath checks that reportOwnerRE follows
// runReportExts: every candidate report name of a ship run and of an execute
// run maps back to its run id.
func TestReportOwner_AcceptsEveryReportPath(t *testing.T) {
	for _, stem := range []string{raTestRunID, "ship-" + raTestRunID} {
		cands := reportPaths(t.TempDir(), stem)
		if len(cands) != len(runReportExts) {
			t.Fatalf("reportPaths(%q) = %v, want one path per format %v", stem, cands, runReportExts)
		}
		for _, p := range cands {
			if id, ok := ReportOwner(filepath.Base(p)); !ok || id != raTestRunID {
				t.Errorf("ReportOwner(%q) = (%q, %v), want (%q, true)", filepath.Base(p), id, ok, raTestRunID)
			}
		}
	}
}

// TestResolveRunArtifacts_StatErrorReturnsErrorAndNoPath checks that a stat
// failure other than a missing file is an error that names the path, and that
// no path comes back with it. A regular file in place of a parent folder makes
// the stat fail with ENOTDIR.
func TestResolveRunArtifacts_StatErrorReturnsErrorAndNoPath(t *testing.T) {
	t.Run("state file", func(t *testing.T) {
		root := t.TempDir()
		blocker := filepath.Join(raRunsDir(root), "blocker")
		raTouch(t, blocker)
		bad := filepath.Join(blocker, "ship-feat-x-20261008T120000Z.json")
		st := &state.State{Path: bad, Root: root, Prefix: "ship", Data: map[string]any{"startedAt": raTestStartedAt}}
		got, err := ResolveRunArtifacts(root, st)
		raWantStatErr(t, got, err, bad)
	})
	t.Run("keep path", func(t *testing.T) {
		root := t.TempDir()
		st := raState(t, root, "ship", "feat/x", map[string]any{"startedAt": raTestStartedAt})
		raTouch(t, raReportsDir(root)) // reports/ is a regular file
		got, err := ResolveRunArtifacts(root, st)
		raWantStatErr(t, got, err, filepath.Join(raReportsDir(root), "ship-"+raTestRunID+"-report.md"))
	})
	t.Run("existingPaths stops at the first error", func(t *testing.T) {
		root := t.TempDir()
		raTouch(t, filepath.Join(root, "file"))
		missing := filepath.Join(root, "missing")
		bad := filepath.Join(root, "file", "child")
		got, err := existingPaths([]string{missing, bad, root})
		if err == nil || !strings.Contains(err.Error(), bad) || !errors.Is(err, syscall.ENOTDIR) {
			t.Errorf("existingPaths error = %v, want ENOTDIR that names %s", err, bad)
		}
		if got != nil {
			t.Errorf("existingPaths = %v, want nil with the error", got)
		}
	})
}

// raWantStatErr checks a ResolveRunArtifacts result that must be a stat error
// for path with no paths.
func raWantStatErr(t *testing.T, got RunArtifacts, err error, path string) {
	t.Helper()
	if err == nil {
		t.Fatalf("ResolveRunArtifacts = %+v, want a stat error", got)
	}
	if want := "resolve run artifacts: stat " + path + ": "; !strings.HasPrefix(err.Error(), want) {
		t.Errorf("error = %q, want prefix %q", err, want)
	}
	if !errors.Is(err, syscall.ENOTDIR) {
		t.Errorf("error = %v, want it to wrap ENOTDIR", err)
	}
	if !reflect.DeepEqual(got, RunArtifacts{}) {
		t.Errorf("ResolveRunArtifacts = %+v, want no paths with the error", got)
	}
}
