package tools

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/fsx"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
	"github.com/rnagrodzki/sdlc-plugin/internal/state"
)

// Fixed names of the archive tests. The ship runs on feat/x from 08:00 to
// 09:00, its execute run starts at 08:01 and its review run at 08:31.
const (
	// archShipFile is the state file name of the ship run.
	archShipFile = "ship-feat-x-20261007T080000Z.json"
	// archShipID is the row id of the ship run.
	archShipID = "ship-feat-x-20261007T080000Z"
	// archExecFile is the state file name of the nested execute run.
	archExecFile = "execute-feat-x-20261007T080100Z.json"
	// archExecID is the row id of the nested execute run.
	archExecID = "execute-feat-x-20261007T080100Z"
	// archExecRun is the execRunID of the nested execute run.
	archExecRun = "20261007T080100"
	// archShipReport is the report file name of the ship run.
	archShipReport = "ship-20261007T080000-report.md"
	// archExecReport is the report file name of the nested execute run.
	archExecReport = "20261007T080100-report.json"
	// archReview is the ledger folder name of the nested review run.
	archReview = "review-2026-10-07T08-31-00Z"
	// archPlanFile is the state file name of the standalone plan run.
	archPlanFile = "plan-feat-y-20261007T070000Z.json"
	// archPlanID is the row id of the standalone plan run.
	archPlanID = "plan-feat-y-20261007T070000Z"
	// archOtherFile is the state file name of the unrelated execute run.
	archOtherFile = "execute-feat-z-20261007T090000Z.json"
	// archOtherID is the row id of the unrelated execute run.
	archOtherID = "execute-feat-z-20261007T090000Z"
)

// archData returns a path below <root>/.sdlc-v2.
func archData(root string, parts ...string) string {
	return filepath.Join(append([]string{root, paths.DataDir}, parts...)...)
}

// archDir returns the archive folder of run id.
func archDir(root, id string) string {
	return archData(root, paths.RunArchiveSubdir, id)
}

// archShipData returns a ship state on feat/x with the given pipeline status
// ("completed" or "" for a running ship).
func archShipData(status string) map[string]any {
	d := map[string]any{
		"branch":    "feat/x",
		"startedAt": "2026-10-07T08:00:00Z",
		"steps": []any{
			map[string]any{"name": "execute", "status": StepCompleted, "startedAt": "2026-10-07T08:00:00Z", "completedAt": "2026-10-07T08:20:00Z"},
			map[string]any{"name": "review", "status": StepCompleted, "startedAt": "2026-10-07T08:30:00Z", "completedAt": "2026-10-07T08:40:00Z"},
		},
	}
	if status == PipelineCompleted {
		d["pipelineStatus"] = "completed"
		d["pipelineCompletedAt"] = "2026-10-07T09:00:00Z"
	} else {
		steps := d["steps"].([]any)
		steps[1].(map[string]any)["status"] = StepInProgress
		delete(steps[1].(map[string]any), "completedAt")
	}
	return d
}

// archExecData returns a completed execute state on branch that started at
// startedAt.
func archExecData(branch, startedAt string) map[string]any {
	return map[string]any{
		"branch":         branch,
		"startedAt":      startedAt,
		"runStatus":      "completed",
		"runCompletedAt": "2026-10-07T08:20:00Z",
	}
}

// archShipFixture writes a completed ship with a nested execute run (state,
// ledger, progress dir, report), a nested review run, the ship report, and an
// unrelated execute run on feat/z that the archive must not touch. mtime sets
// the state file times, so a test can make the ship stalled.
func archShipFixture(t *testing.T, shipStatus string, mtime time.Time) string {
	t.Helper()
	root := dashRoot(t)
	dashWriteState(t, root, archShipFile, archShipData(shipStatus), mtime)
	dashWriteState(t, root, archExecFile, archExecData("feat/x", "2026-10-07T08:01:00Z"), mtime)
	writeFile(t, archData(root, "runs", "ledger", archExecRun, "w1.json"), `{}`)
	writeFile(t, archData(root, "runs", archExecRun, "progress", "1.json"), `{}`)
	writeFile(t, archData(root, "reports", archExecReport), `{}`)
	writeFile(t, archData(root, "reports", archShipReport), "# report")
	dashWriteReviewDim(t, root, archReview, "security", map[string]any{
		"checkinAt": "2026-10-07T08:31:00Z", "checkoutAt": "2026-10-07T08:35:00Z",
	}, mtime)
	dashJoinRunMeta(t, root, archReview, reviewRunMeta{Branch: "feat/x", StartedAt: "2026-10-07T08:31:00Z", ShipRunID: archShipID})
	dashWriteState(t, root, archOtherFile, archExecData("feat/z", "2026-10-07T09:00:00Z"), mtime)
	writeFile(t, archData(root, "reports", "20261007T090000-report.md"), "# other")
	return root
}

// archRun calls ArchiveRun at dashNow.
func archRun(root, id string, confirm bool) (ArchiveRunOut, error) {
	return ArchiveRun(ArchiveRunIn{Root: root, RunID: id, ConfirmStalled: confirm}, dashNow)
}

// archCode returns the code of an ArchiveError and fails the test when err is
// not one or has an empty suggestion.
func archCode(t *testing.T, err error) string {
	t.Helper()
	var ae *ArchiveError
	if !errors.As(err, &ae) {
		t.Fatalf("err = %v (%T), want *ArchiveError", err, err)
	}
	if ae.Suggestion == "" {
		t.Errorf("error %q has an empty suggestion", ae.Code)
	}
	return ae.Code
}

// archRowIDs returns the sorted ids of the pipeline rows of root at dashNow.
func archRowIDs(t *testing.T, root string) []string {
	t.Helper()
	ids := []string{}
	for _, p := range dashCollect(t, root).Pipelines {
		ids = append(ids, p.ID)
	}
	sort.Strings(ids)
	return ids
}

// archReadRecord decodes archive.json of run id into a generic map.
func archReadRecord(t *testing.T, root, id string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(archDir(root, id), archiveRecordFile))
	if err != nil {
		t.Fatalf("read archive.json: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("decode archive.json: %v", err)
	}
	return m
}

// archSeam replaces archiveRename and archiveRemoveAll for one test and
// restores both when it ends. A test that calls it must not run in parallel.
func archSeam(t *testing.T) {
	t.Helper()
	origRename, origRemove := archiveRename, archiveRemoveAll
	t.Cleanup(func() { archiveRename, archiveRemoveAll = origRename, origRemove })
}

// archFailRename makes archiveRename fail for a source whose base name is name.
func archFailRename(t *testing.T, name string) {
	t.Helper()
	archSeam(t)
	archiveRename = func(src, dst string) error {
		if filepath.Base(src) == name {
			return errors.New("permission denied")
		}
		return os.Rename(src, dst)
	}
}

// archRestore puts the real archiveRename and archiveRemoveAll back.
func archRestore() {
	archiveRename, archiveRemoveAll = os.Rename, os.RemoveAll
}

// TestArchiveRun_ShipMovesMembers checks a ship archive: the ship and execute
// state files, both ledgers and both reports move, the execute progress dir is
// deleted, the unrelated run stays, and the next snapshot lists no archived
// run.
func TestArchiveRun_ShipMovesMembers(t *testing.T) {
	root := archShipFixture(t, PipelineCompleted, dashJoinFresh)
	if got, want := archRowIDs(t, root), []string{archOtherID, archShipID}; !reflect.DeepEqual(got, want) {
		t.Fatalf("rows before = %v, want %v", got, want)
	}

	out, err := archRun(root, archShipID, false)
	if err != nil {
		t.Fatalf("ArchiveRun: %v", err)
	}
	dir := archDir(root, archShipID)
	if out.RunID != archShipID || out.Dir != dir {
		t.Errorf("out = %+v, want runId %q dir %q", out, archShipID, dir)
	}
	wantMoved := []string{
		"runs/ledger/" + archReview,
		"runs/ledger/" + archExecRun,
		"reports/" + archExecReport,
		"reports/" + archShipReport,
		"runs/" + archExecFile,
	}
	sortedMoved := append([]string{}, out.Moved...)
	sort.Strings(sortedMoved)
	wantAll := append(append([]string{}, wantMoved...), "runs/"+archShipFile)
	sort.Strings(wantAll)
	if !reflect.DeepEqual(sortedMoved, wantAll) {
		t.Errorf("moved = %v, want %v", out.Moved, wantAll)
	}
	if last := out.Moved[len(out.Moved)-1]; last != "runs/"+archShipFile {
		t.Errorf("last move = %q, want the root state file", last)
	}
	if got, want := out.Deleted, []string{"runs/" + archExecRun}; !reflect.DeepEqual(got, want) {
		t.Errorf("deleted = %v, want %v", got, want)
	}

	for _, p := range []string{
		archShipFile, archExecFile,
		filepath.Join("ledger", archExecRun, "w1.json"),
		filepath.Join("ledger", archReview, "security.json"),
		filepath.Join("ledger", archReview, ledgerRunMetaFile),
		filepath.Join("reports", archShipReport),
		filepath.Join("reports", archExecReport),
		archiveRecordFile,
	} {
		if !ccExists(filepath.Join(dir, p)) {
			t.Errorf("archive lacks %s", p)
		}
	}
	for _, p := range []string{
		archData(root, "runs", archShipFile), archData(root, "runs", archExecFile),
		archData(root, "runs", archExecRun), archData(root, "runs", "ledger", archExecRun),
		archData(root, "runs", "ledger", archReview), archData(root, "reports", archShipReport),
	} {
		if ccExists(p) {
			t.Errorf("%s still exists after the archive", p)
		}
	}
	if ccExists(filepath.Join(dir, archExecRun)) {
		t.Errorf("the Working dir was moved into the archive, want it deleted")
	}
	if !ccExists(archData(root, "runs", archOtherFile)) || !ccExists(archData(root, "reports", "20261007T090000-report.md")) {
		t.Errorf("the archive touched the unrelated run")
	}

	if got, want := archRowIDs(t, root), []string{archOtherID}; !reflect.DeepEqual(got, want) {
		t.Errorf("rows after = %v, want %v", got, want)
	}

	rec := archReadRecord(t, root, archShipID)
	keys := []string{}
	for k := range rec {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if want := []string{"archivedAt", "deleted", "moved", "runId"}; !reflect.DeepEqual(keys, want) {
		t.Errorf("archive.json keys = %v, want %v", keys, want)
	}
	if rec["runId"] != archShipID || rec["archivedAt"] != "2026-10-07T10:00:00Z" {
		t.Errorf("archive.json = %v", rec)
	}
	recMoved := []string{}
	for _, v := range rec["moved"].([]any) {
		recMoved = append(recMoved, v.(string))
	}
	sort.Strings(recMoved)
	sort.Strings(wantMoved)
	if !reflect.DeepEqual(recMoved, wantMoved) {
		t.Errorf("archive.json moved = %v, want %v (all moves but the root state file)", recMoved, wantMoved)
	}
}

// TestArchiveRun_ReviewMovesLedgerOnly checks a standalone review archive: it
// moves only its ledger folder, and archive.json and the result encode empty
// lists as [].
func TestArchiveRun_ReviewMovesLedgerOnly(t *testing.T) {
	root := dashRoot(t)
	dashWriteReviewDim(t, root, archReview, "security", map[string]any{
		"checkinAt": "2026-10-07T08:31:00Z", "checkoutAt": "2026-10-07T08:35:00Z",
	}, dashJoinFresh)
	writeFile(t, archData(root, "reports", archShipReport), "# report")

	out, err := archRun(root, archReview, false)
	if err != nil {
		t.Fatalf("ArchiveRun: %v", err)
	}
	if got, want := out.Moved, []string{"runs/ledger/" + archReview}; !reflect.DeepEqual(got, want) {
		t.Errorf("moved = %v, want %v", got, want)
	}
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"deleted":[]`) {
		t.Errorf("result JSON = %s, want deleted []", b)
	}
	if !ccExists(filepath.Join(archDir(root, archReview), "ledger", archReview, "security.json")) {
		t.Errorf("ledger folder not in the archive")
	}
	if !ccExists(archData(root, "reports", archShipReport)) {
		t.Errorf("a review archive moved a report")
	}
	raw, err := os.ReadFile(filepath.Join(archDir(root, archReview), archiveRecordFile))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"moved": []`, `"deleted": []`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("archive.json = %s, want %s", raw, want)
		}
	}
	if got := archRowIDs(t, root); len(got) != 0 {
		t.Errorf("rows after = %v, want none", got)
	}
}

// TestArchiveRun_PlanMovesBriefDeletesEvidence checks a plan archive: the
// brief goes to evidence/brief.md and the evidence folder is deleted.
func TestArchiveRun_PlanMovesBriefDeletesEvidence(t *testing.T) {
	root := dashRoot(t)
	dashWriteState(t, root, archPlanFile, map[string]any{
		"branch": "feat/y", "planIntegrity": map[string]any{"done": "2026-10-07T07:30:00Z"},
	}, dashJoinFresh)
	evidence := archData(root, "runs", archPlanID+".evidence")
	writeFile(t, filepath.Join(evidence, "brief.md"), "# brief")
	writeFile(t, filepath.Join(evidence, "explore.jsonl"), "{}")

	out, err := archRun(root, archPlanID, false)
	if err != nil {
		t.Fatalf("ArchiveRun: %v", err)
	}
	if got, want := out.Moved, []string{"runs/" + archPlanID + ".evidence/brief.md", "runs/" + archPlanFile}; !reflect.DeepEqual(got, want) {
		t.Errorf("moved = %v, want %v", got, want)
	}
	if got, want := out.Deleted, []string{"runs/" + archPlanID + ".evidence"}; !reflect.DeepEqual(got, want) {
		t.Errorf("deleted = %v, want %v", got, want)
	}
	dir := archDir(root, archPlanID)
	for _, p := range []string{filepath.Join("evidence", "brief.md"), archPlanFile} {
		if !ccExists(filepath.Join(dir, p)) {
			t.Errorf("archive lacks %s", p)
		}
	}
	if ccExists(evidence) {
		t.Errorf("evidence folder still exists")
	}
}

// TestArchiveRun_StatusGate checks the row status rule: running refuses,
// stalled needs a confirm, a just-completed row archives at once.
func TestArchiveRun_StatusGate(t *testing.T) {
	t.Run("running", func(t *testing.T) {
		root := archShipFixture(t, "", dashJoinFresh)
		_, err := archRun(root, archShipID, true)
		if code := archCode(t, err); code != ArchiveRunActive {
			t.Errorf("code = %q, want %q", code, ArchiveRunActive)
		}
		if ccExists(archData(root, paths.RunArchiveSubdir)) {
			t.Errorf("a refused archive created run-archive/")
		}
	})
	t.Run("stalled without confirm", func(t *testing.T) {
		root := archShipFixture(t, "", dashNow.Add(-2*time.Hour))
		_, err := archRun(root, archShipID, false)
		if code := archCode(t, err); code != ArchiveConfirmStalled {
			t.Errorf("code = %q, want %q", code, ArchiveConfirmStalled)
		}
		if ccExists(archData(root, paths.RunArchiveSubdir)) || !ccExists(archData(root, "runs", archShipFile)) {
			t.Errorf("a refused archive changed a file")
		}
	})
	t.Run("stalled with confirm", func(t *testing.T) {
		root := archShipFixture(t, "", dashNow.Add(-2*time.Hour))
		if _, err := archRun(root, archShipID, true); err != nil {
			t.Fatalf("ArchiveRun: %v", err)
		}
		if !ccExists(filepath.Join(archDir(root, archShipID), archShipFile)) {
			t.Errorf("ship state not archived")
		}
	})
	t.Run("just completed", func(t *testing.T) {
		root := archShipFixture(t, PipelineCompleted, dashNow)
		if _, err := archRun(root, archShipID, false); err != nil {
			t.Fatalf("ArchiveRun: %v", err)
		}
	})
}

// TestArchiveRun_BadAndUnknownID checks that a bad id gives BAD_RUN_ID, an
// unknown or nested id gives RUN_NOT_FOUND, and neither changes a file.
func TestArchiveRun_BadAndUnknownID(t *testing.T) {
	cases := []struct {
		id   string
		code string
	}{
		{"", ArchiveBadRunID},
		{".", ArchiveBadRunID},
		{"..", ArchiveBadRunID},
		{"a/b", ArchiveBadRunID},
		{`ship-x\y`, ArchiveBadRunID},
		{"../" + archShipID, ArchiveBadRunID},
		{"foo-main-20261007T080000Z", ArchiveBadRunID},
		{"review-", ArchiveBadRunID},
		{"ship-", ArchiveBadRunID},
		{"ship-feat-x-20991231T000000Z", ArchiveRunNotFound},
		{"review-2099-01-01T00-00-00Z", ArchiveRunNotFound},
		{archExecID, ArchiveRunNotFound}, // nested in the ship: moves only with it
	}
	root := archShipFixture(t, PipelineCompleted, dashJoinFresh)
	before := archTree(t, root)
	for _, c := range cases {
		out, err := archRun(root, c.id, false)
		if code := archCode(t, err); code != c.code {
			t.Errorf("id %q: code = %q, want %q", c.id, code, c.code)
		}
		if out.Moved == nil || out.Deleted == nil {
			t.Errorf("id %q: moved/deleted nil, want []", c.id)
		}
	}
	if after := archTree(t, root); !reflect.DeepEqual(before, after) {
		t.Errorf("a refused archive changed files:\nbefore %v\nafter  %v", before, after)
	}
}

// archTree returns every path below <root>/.sdlc-v2, relative to root.
func archTree(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	err := filepath.Walk(archData(root), func(p string, _ os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		out = append(out, rel)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// archAssertRetry checks the state after a failed archive: the code is
// ARCHIVE_FAILED, the ship state is still in runs/, the ship is still a row,
// and an archive with the real seams then succeeds and removes the row.
func archAssertRetry(t *testing.T, root string, err error) {
	t.Helper()
	if code := archCode(t, err); code != ArchiveFailed {
		t.Fatalf("code = %q, want %q", code, ArchiveFailed)
	}
	if !ccExists(archData(root, "runs", archShipFile)) {
		t.Fatalf("ship state left runs/ after a failed archive")
	}
	rows := archRowIDs(t, root)
	found := false
	for _, id := range rows {
		found = found || id == archShipID
	}
	if !found {
		t.Fatalf("ship row gone after a failed archive: %v", rows)
	}
	archRestore()
	if _, err := archRun(root, archShipID, false); err != nil {
		t.Fatalf("retry ArchiveRun: %v", err)
	}
	if got, want := archRowIDs(t, root), []string{archOtherID}; !reflect.DeepEqual(got, want) {
		t.Errorf("rows after retry = %v, want %v", got, want)
	}
	if !ccExists(filepath.Join(archDir(root, archShipID), archShipFile)) {
		t.Errorf("ship state not in the archive after the retry")
	}
}

// TestArchiveRun_Step1MkdirFails checks a failure to create the archive
// folder: nothing moves.
func TestArchiveRun_Step1MkdirFails(t *testing.T) {
	root := archShipFixture(t, PipelineCompleted, dashJoinFresh)
	block := archData(root, paths.RunArchiveSubdir)
	writeFile(t, block, "not a folder")
	_, err := archRun(root, archShipID, false)
	if !ccExists(archData(root, "runs", archExecFile)) || !ccExists(archData(root, "runs", "ledger", archExecRun)) {
		t.Errorf("a file moved before the archive folder existed")
	}
	var dl *fsx.DanglingLinkError
	if errors.As(err, &dl) {
		t.Errorf("a regular file blocker gave a dangling link error: %v", dl)
	}
	var ae *ArchiveError
	if !errors.As(err, &ae) {
		t.Fatalf("err = %v (%T), want *ArchiveError", err, err)
	}
	if ae.Suggestion != archiveSuggestFSFailed {
		t.Errorf("suggestion = %q, want %q", ae.Suggestion, archiveSuggestFSFailed)
	}
	if err := os.Remove(block); err != nil {
		t.Fatal(err)
	}
	archAssertRetry(t, root, err)
}

// TestArchiveRun_Step1DanglingLink checks that a run-archive link to a missing
// folder gives ARCHIVE_FAILED with the recovery text of the link error as the
// suggestion, and that nothing moves.
func TestArchiveRun_Step1DanglingLink(t *testing.T) {
	root := archShipFixture(t, PipelineCompleted, dashJoinFresh)
	link := archData(root, paths.RunArchiveSubdir)
	target := filepath.Join(t.TempDir(), "main", paths.DataDir, paths.RunArchiveSubdir)
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	_, err := archRun(root, archShipID, false)
	if !ccExists(archData(root, "runs", archExecFile)) || !ccExists(archData(root, "runs", "ledger", archExecRun)) {
		t.Errorf("a file moved before the archive folder existed")
	}
	var dl *fsx.DanglingLinkError
	if !errors.As(err, &dl) {
		t.Fatalf("err = %v (%T), want a wrapped *fsx.DanglingLinkError", err, err)
	}
	var ae *ArchiveError
	if !errors.As(err, &ae) {
		t.Fatalf("err = %v (%T), want *ArchiveError", err, err)
	}
	if want := "mkdir -p " + target; !strings.Contains(ae.Message, link) || !strings.Contains(ae.Message, target) || !strings.Contains(ae.Message, want) {
		t.Errorf("message %q lacks the link %q, the target %q, or %q", ae.Message, link, target, want)
	}
	if ae.Suggestion != dl.Recovery() {
		t.Errorf("suggestion = %q, want %q", ae.Suggestion, dl.Recovery())
	}
	if ae.Suggestion == archiveSuggestFSFailed {
		t.Errorf("suggestion is the permission text: %q", ae.Suggestion)
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	archAssertRetry(t, root, err)
}

// TestArchiveRun_Step2KeepMoveFails checks a move failure of a member Keep
// path: the state files stay in runs/.
func TestArchiveRun_Step2KeepMoveFails(t *testing.T) {
	root := archShipFixture(t, PipelineCompleted, dashJoinFresh)
	archFailRename(t, archExecRun)
	_, err := archRun(root, archShipID, false)
	if !ccExists(archData(root, "runs", "ledger", archExecRun)) {
		t.Errorf("execute ledger moved despite the failure")
	}
	if !ccExists(archData(root, "runs", archExecFile)) || !ccExists(archData(root, "runs", archExecRun)) {
		t.Errorf("a later step ran after the step 2 failure")
	}
	archAssertRetry(t, root, err)
}

// TestArchiveRun_Step3DeleteFails checks a delete failure of a Working path:
// the Keep paths moved, the state files stay in runs/.
func TestArchiveRun_Step3DeleteFails(t *testing.T) {
	root := archShipFixture(t, PipelineCompleted, dashJoinFresh)
	archSeam(t)
	archiveRemoveAll = func(string) error { return errors.New("permission denied") }
	_, err := archRun(root, archShipID, false)
	if ccExists(archData(root, "runs", "ledger", archExecRun)) {
		t.Errorf("execute ledger not moved before the step 3 failure")
	}
	if !ccExists(archData(root, "runs", archExecRun)) || !ccExists(archData(root, "runs", archExecFile)) {
		t.Errorf("progress dir or execute state gone after the step 3 failure")
	}
	archAssertRetry(t, root, err)
	if ccExists(archData(root, "runs", archExecRun)) {
		t.Errorf("progress dir not deleted by the retry")
	}
}

// TestArchiveRun_Step4MemberStateFails checks a move failure of a member state
// file: the Working dir is gone, the member and root state files stay.
func TestArchiveRun_Step4MemberStateFails(t *testing.T) {
	root := archShipFixture(t, PipelineCompleted, dashJoinFresh)
	archFailRename(t, archExecFile)
	_, err := archRun(root, archShipID, false)
	if ccExists(archData(root, "runs", archExecRun)) {
		t.Errorf("progress dir not deleted before the step 4 failure")
	}
	if !ccExists(archData(root, "runs", archExecFile)) {
		t.Errorf("execute state moved despite the failure")
	}
	if ccExists(filepath.Join(archDir(root, archShipID), archiveRecordFile)) {
		t.Errorf("archive.json written after the step 4 failure")
	}
	archAssertRetry(t, root, err)
}

// TestArchiveRun_Step5RecordFails checks a write failure of archive.json: the
// member state files moved, the root state file stays in runs/.
func TestArchiveRun_Step5RecordFails(t *testing.T) {
	root := archShipFixture(t, PipelineCompleted, dashJoinFresh)
	block := filepath.Join(archDir(root, archShipID), archiveRecordFile)
	writeFile(t, filepath.Join(block, "keep"), "x")
	_, err := archRun(root, archShipID, false)
	if ccExists(archData(root, "runs", archExecFile)) {
		t.Errorf("execute state not moved before the step 5 failure")
	}
	if err := os.RemoveAll(block); err != nil {
		t.Fatal(err)
	}
	archAssertRetry(t, root, err)
}

// TestArchiveRun_Step6RootStateFails checks a move failure of the root state
// file: everything else moved, the root state file stays in runs/.
func TestArchiveRun_Step6RootStateFails(t *testing.T) {
	root := archShipFixture(t, PipelineCompleted, dashJoinFresh)
	archFailRename(t, archShipFile)
	_, err := archRun(root, archShipID, false)
	if ccExists(archData(root, "runs", archExecFile)) || ccExists(archData(root, "runs", "ledger", archReview)) {
		t.Errorf("members not moved before the step 6 failure")
	}
	if !ccExists(filepath.Join(archDir(root, archShipID), archiveRecordFile)) {
		t.Errorf("archive.json not written before the step 6 failure")
	}
	archAssertRetry(t, root, err)
}

// TestArchiveRun_ReviewLedgerMovesAfterRecord checks that a review archive
// writes archive.json before it moves the ledger folder: a move failure keeps
// the review a row with archive.json in place.
func TestArchiveRun_ReviewLedgerMovesAfterRecord(t *testing.T) {
	root := dashRoot(t)
	dashWriteReviewDim(t, root, archReview, "security", map[string]any{
		"checkinAt": "2026-10-07T08:31:00Z", "checkoutAt": "2026-10-07T08:35:00Z",
	}, dashJoinFresh)
	archFailRename(t, archReview)
	_, err := archRun(root, archReview, false)
	if code := archCode(t, err); code != ArchiveFailed {
		t.Fatalf("code = %q, want %q", code, ArchiveFailed)
	}
	if !ccExists(filepath.Join(archDir(root, archReview), archiveRecordFile)) {
		t.Errorf("archive.json not written before the ledger move")
	}
	if got, want := archRowIDs(t, root), []string{archReview}; !reflect.DeepEqual(got, want) {
		t.Errorf("rows = %v, want %v", got, want)
	}
}

// TestArchiveDestRel checks the source to archive path mapping.
func TestArchiveDestRel(t *testing.T) {
	cases := map[string]string{
		"runs/ship-x-1.json":              "ship-x-1.json",
		"runs/ledger/20261008T120000":     "ledger/20261008T120000",
		"runs/ledger/review-a":            "ledger/review-a",
		"runs/plan-x-1.evidence/brief.md": "evidence/brief.md",
		"reports/ship-1-report.md":        "reports/ship-1-report.md",
	}
	for in, want := range cases {
		if got := filepath.ToSlash(archiveDestRel(filepath.FromSlash(in))); got != want {
			t.Errorf("archiveDestRel(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestArchiveJoinMembers checks that the join records the nested execute id
// and review name on the ship row.
func TestArchiveJoinMembers(t *testing.T) {
	root := archShipFixture(t, PipelineCompleted, dashJoinFresh)
	repo := dashCollect(t, root)
	ship := dashJoinFind(t, repo.Pipelines, archShipID)
	got := append([]string{}, ship.join.members...)
	sort.Strings(got)
	if want := []string{archExecID, archReview}; !reflect.DeepEqual(got, want) {
		t.Errorf("members = %v, want %v", got, want)
	}
}

// archAssertUnchanged fails the test when the tree below <root>/.sdlc-v2
// differs from before or the archive folder exists.
func archAssertUnchanged(t *testing.T, root string, before []string) {
	t.Helper()
	if after := archTree(t, root); !reflect.DeepEqual(before, after) {
		t.Errorf("a failed check changed files:\nbefore %v\nafter  %v", before, after)
	}
	if ccExists(archData(root, paths.RunArchiveSubdir)) {
		t.Errorf("a failed check created run-archive/")
	}
}

// archShipOnly writes a completed ship state with its report and returns the
// root. The ship has no member run.
func archShipOnly(t *testing.T) string {
	t.Helper()
	root := dashRoot(t)
	dashWriteState(t, root, archShipFile, archShipData(PipelineCompleted), dashJoinFresh)
	writeFile(t, archData(root, "reports", archShipReport), "# report")
	return root
}

// TestArchiveRun_CheckErrorsChangeNoFile checks the check phase of ArchiveRun:
// each failed check returns its code, names the cause in the message, and
// changes no file.
func TestArchiveRun_CheckErrorsChangeNoFile(t *testing.T) {
	t.Run("repo path is a regular file", func(t *testing.T) {
		base := t.TempDir()
		root := filepath.Join(base, "repo")
		writeFile(t, root, "not a folder")
		_, err := archRun(root, archShipID, false)
		if code := archCode(t, err); code != ArchiveFailed {
			t.Fatalf("code = %q, want %q", code, ArchiveFailed)
		}
		var ae *ArchiveError
		errors.As(err, &ae)
		if want := "Read the runs of repo " + root + ": repo path is not a folder: " + root; ae.Message != want {
			t.Errorf("message = %q, want %q", ae.Message, want)
		}
		if b, rerr := os.ReadFile(root); rerr != nil || string(b) != "not a folder" {
			t.Errorf("repo file changed: %q, %v", b, rerr)
		}
	})
	t.Run("runs is a regular file", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, archData(root, "runs"), "not a folder")
		before := archTree(t, root)
		_, err := archRun(root, archShipID, false)
		if code := archCode(t, err); code != ArchiveFailed {
			t.Fatalf("code = %q, want %q", code, ArchiveFailed)
		}
		if !strings.Contains(err.Error(), archData(root, "runs")) {
			t.Errorf("error %q does not name the runs folder", err)
		}
		archAssertUnchanged(t, root, before)
	})
	t.Run("root Keep path cannot be read", func(t *testing.T) {
		root := dashRoot(t)
		dashWriteState(t, root, archShipFile, archShipData(PipelineCompleted), dashJoinFresh)
		writeFile(t, archData(root, "reports"), "not a folder")
		before := archTree(t, root)
		_, err := archRun(root, archShipID, false)
		if code := archCode(t, err); code != ArchiveFailed {
			t.Fatalf("code = %q, want %q", code, ArchiveFailed)
		}
		if !errors.Is(err, syscall.ENOTDIR) {
			t.Errorf("err = %v, want it to wrap ENOTDIR", err)
		}
		if !strings.Contains(err.Error(), archData(root, "reports", archShipReport)) {
			t.Errorf("error %q does not name the report path", err)
		}
		archAssertUnchanged(t, root, before)
	})
	t.Run("member Keep path cannot be read", func(t *testing.T) {
		root := archShipOnly(t)
		dashWriteState(t, root, archExecFile, archExecData("feat/x", "2026-10-07T08:01:00Z"), dashJoinFresh)
		writeFile(t, archData(root, "runs", "ledger"), "not a folder")
		ship := dashJoinFind(t, dashCollect(t, root).Pipelines, archShipID)
		if !reflect.DeepEqual(ship.join.members, []string{archExecID}) {
			t.Fatalf("members = %v, want [%s]", ship.join.members, archExecID)
		}
		before := archTree(t, root)
		_, err := archRun(root, archShipID, false)
		if code := archCode(t, err); code != ArchiveFailed {
			t.Fatalf("code = %q, want %q", code, ArchiveFailed)
		}
		if !errors.Is(err, syscall.ENOTDIR) {
			t.Errorf("err = %v, want it to wrap ENOTDIR", err)
		}
		if !strings.Contains(err.Error(), archData(root, "runs", "ledger", archExecRun)) {
			t.Errorf("error %q does not name the ledger path", err)
		}
		archAssertUnchanged(t, root, before)
	})
}

// TestArchiveResolve_Errors checks the arms of archiveResolve that ArchiveRun
// reaches only when runs/ changes after the collect: a review ledger that
// cannot be read, an id with no state, and a state whose file is gone.
func TestArchiveResolve_Errors(t *testing.T) {
	root := dashRoot(t)
	writeFile(t, archData(root, "runs", "ledger"), "not a folder")
	shipState := &state.State{
		Path:   archData(root, "runs", archShipFile), // never written
		Root:   root,
		Prefix: "ship",
		Data:   archShipData(PipelineCompleted),
	}
	cases := []struct {
		name, id string
		states   []*state.State
		code     string
		msg      string
	}{
		{"bad review name", "review-", nil, ArchiveBadRunID, `Invalid review run name "review-"`},
		{"review ledger cannot be read", archReview, nil, ArchiveFailed, "Resolve run artifacts: stat " + archData(root, "runs", "ledger", archReview)},
		{"no state for the id", archShipID, nil, ArchiveRunNotFound, fmt.Sprintf("No state file for %q", archShipID)},
		{"state file is gone", archShipID, []*state.State{shipState}, ArchiveRunNotFound, fmt.Sprintf("No state file for %q", archShipID)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			files, err := archiveResolve(root, c.states, c.id)
			if code := archCode(t, err); code != c.code {
				t.Errorf("code = %q, want %q", code, c.code)
			}
			var ae *ArchiveError
			errors.As(err, &ae)
			if !strings.HasPrefix(ae.Message, c.msg) {
				t.Errorf("message = %q, want prefix %q", ae.Message, c.msg)
			}
			if !reflect.DeepEqual(files, archiveRunFiles{}) {
				t.Errorf("files = %+v, want none", files)
			}
		})
	}
	t.Run("review ledger is gone", func(t *testing.T) {
		_, err := archiveResolve(dashRoot(t), nil, archReview)
		if code := archCode(t, err); code != ArchiveRunNotFound {
			t.Errorf("code = %q, want %q", code, ArchiveRunNotFound)
		}
		if want := fmt.Sprintf("%s: No review ledger folder for %q", ArchiveRunNotFound, archReview); err.Error() != want {
			t.Errorf("error = %q, want %q", err, want)
		}
	})
}

// TestArchiveMover_RejectsPathsOutsideData checks the containment guard of
// archiveMover: rel, move and remove refuse a path that is not below
// .sdlc-v2/ with ARCHIVE_FAILED and change no file.
func TestArchiveMover_RejectsPathsOutsideData(t *testing.T) {
	root := dashRoot(t)
	data := archData(root)
	outside := filepath.Join(root, "outside.txt")
	writeFile(t, outside, "keep me")
	sibling := filepath.Join(root, ".sdlc-v2-other", "x.json")
	writeFile(t, sibling, "{}")
	cases := map[string]string{
		"relative path":         filepath.Join("runs", archShipFile),
		"the data folder":       data,
		"the repo root":         root,
		"a file beside data":    outside,
		"a sibling folder":      sibling,
		"dot dot segment":       data + string(filepath.Separator) + ".." + string(filepath.Separator) + "outside.txt",
		"dot dot past the root": data + string(filepath.Separator) + ".." + string(filepath.Separator) + "..",
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			before := archTree(t, root)
			m := &archiveMover{data: data, dir: archDir(root, archShipID), moved: []string{}, deleted: []string{}}
			if _, err := m.rel(src); archCode(t, err) != ArchiveFailed {
				t.Errorf("rel: code = %q, want %q", archCode(t, err), ArchiveFailed)
			} else if want := "Path " + src + " is outside " + data; err.Error() != ArchiveFailed+": "+want {
				t.Errorf("rel: error = %q, want %q", err, ArchiveFailed+": "+want)
			}
			if err := m.move(src); archCode(t, err) != ArchiveFailed {
				t.Errorf("move: code = %q, want %q", archCode(t, err), ArchiveFailed)
			}
			if err := m.remove(src); archCode(t, err) != ArchiveFailed {
				t.Errorf("remove: code = %q, want %q", archCode(t, err), ArchiveFailed)
			}
			if len(m.moved) != 0 || len(m.deleted) != 0 {
				t.Errorf("moved %v, deleted %v, want none", m.moved, m.deleted)
			}
			if ccExists(m.dir) {
				t.Errorf("move created %s for a refused path", m.dir)
			}
			if after := archTree(t, root); !reflect.DeepEqual(before, after) {
				t.Errorf("a refused path changed files:\nbefore %v\nafter  %v", before, after)
			}
			for _, p := range []string{outside, sibling} {
				if !ccExists(p) {
					t.Errorf("%s is gone", p)
				}
			}
		})
	}
}

// TestArchiveRun_FailedShipRow checks that a ship row with status failed
// archives: its state file and report move.
func TestArchiveRun_FailedShipRow(t *testing.T) {
	root := dashRoot(t)
	dashWriteState(t, root, archShipFile, map[string]any{
		"branch":    "feat/x",
		"startedAt": "2026-10-07T08:00:00Z",
		"steps":     []any{map[string]any{"name": "execute", "status": StepFailed, "error": "boom"}},
	}, dashJoinFresh)
	writeFile(t, archData(root, "reports", archShipReport), "# report")
	row := dashJoinFind(t, dashCollect(t, root).Pipelines, archShipID)
	if row.Status != PipelineFailed {
		t.Fatalf("row status = %q, want %q", row.Status, PipelineFailed)
	}

	out, err := archRun(root, archShipID, false)
	if err != nil {
		t.Fatalf("ArchiveRun: %v", err)
	}
	if want := []string{"reports/" + archShipReport, "runs/" + archShipFile}; !reflect.DeepEqual(out.Moved, want) {
		t.Errorf("moved = %v, want %v", out.Moved, want)
	}
	for _, p := range []string{archShipFile, filepath.Join("reports", archShipReport), archiveRecordFile} {
		if !ccExists(filepath.Join(archDir(root, archShipID), p)) {
			t.Errorf("archive lacks %s", p)
		}
	}
	if got := archRowIDs(t, root); len(got) != 0 {
		t.Errorf("rows after = %v, want none", got)
	}
}

// TestArchiveRun_StandaloneExecuteRow checks that an execute row that no ship
// nests archives on its own: its state file and report move, and the ship
// and its members stay.
func TestArchiveRun_StandaloneExecuteRow(t *testing.T) {
	root := archShipFixture(t, PipelineCompleted, dashJoinFresh)
	otherReport := "20261007T090000-report.md"

	out, err := archRun(root, archOtherID, false)
	if err != nil {
		t.Fatalf("ArchiveRun: %v", err)
	}
	if want := []string{"reports/" + otherReport, "runs/" + archOtherFile}; !reflect.DeepEqual(out.Moved, want) {
		t.Errorf("moved = %v, want %v", out.Moved, want)
	}
	if len(out.Deleted) != 0 {
		t.Errorf("deleted = %v, want none", out.Deleted)
	}
	for _, p := range []string{archOtherFile, filepath.Join("reports", otherReport), archiveRecordFile} {
		if !ccExists(filepath.Join(archDir(root, archOtherID), p)) {
			t.Errorf("archive lacks %s", p)
		}
	}
	if !ccExists(archData(root, "runs", archShipFile)) || !ccExists(archData(root, "runs", archExecFile)) {
		t.Errorf("the archive of the execute row touched the ship")
	}
	if got, want := archRowIDs(t, root), []string{archShipID}; !reflect.DeepEqual(got, want) {
		t.Errorf("rows after = %v, want %v", got, want)
	}
}

// TestArchiveError_TextPerCode pins the message, the suggestion and the
// Error() text of one ArchiveError of each code.
func TestArchiveError_TextPerCode(t *testing.T) {
	running := archShipFixture(t, "", dashJoinFresh)
	stalled := archShipFixture(t, "", dashNow.Add(-2*time.Hour))
	notDir := filepath.Join(t.TempDir(), "repo")
	writeFile(t, notDir, "x")

	cases := []struct {
		name       string
		run        func() error
		code, msg  string
		suggestion string
	}{
		{"bad run id", func() error { _, err := archRun(running, "a/b", false); return err },
			ArchiveBadRunID, `Invalid run name "a/b": must not contain a path separator`,
			"Pass the run id from the pipeline row."},
		{"run not found", func() error { _, err := archRun(running, "ship-feat-x-20991231T000000Z", false); return err },
			ArchiveRunNotFound, `No pipeline row with id "ship-feat-x-20991231T000000Z"`,
			"Reload the page. The run is gone."},
		{"run active", func() error { _, err := archRun(running, archShipID, true); return err },
			ArchiveRunActive, `Run "` + archShipID + `" is running`,
			"Wait until the run ends or stalls."},
		{"confirm stalled", func() error { _, err := archRun(stalled, archShipID, false); return err },
			ArchiveConfirmStalled, `Run "` + archShipID + `" is stalled and the archive is not confirmed`,
			"Confirm. Archive removes the resume point."},
		{"read failed", func() error { _, err := archRun(notDir, archShipID, false); return err },
			ArchiveFailed, "Read the runs of repo " + notDir + ": repo path is not a folder: " + notDir,
			"Fix the run files named in the message, then archive again."},
		{"move failed", func() error {
			archFailRename(t, archShipFile)
			defer archRestore()
			_, err := archRun(stalled, archShipID, true)
			return err
		}, ArchiveFailed, "Move " + archData(stalled, "runs", archShipFile) + ": permission denied",
			"Fix the permission of the named file, then archive again."},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.run()
			var ae *ArchiveError
			if !errors.As(err, &ae) {
				t.Fatalf("err = %v (%T), want *ArchiveError", err, err)
			}
			if ae.Code != c.code || ae.Message != c.msg || ae.Suggestion != c.suggestion {
				t.Errorf("error = {%q, %q, %q}, want {%q, %q, %q}", ae.Code, ae.Message, ae.Suggestion, c.code, c.msg, c.suggestion)
			}
			if want := c.code + ": " + c.msg; err.Error() != want {
				t.Errorf("Error() = %q, want %q", err.Error(), want)
			}
		})
	}
}

// TestArchiveErr_CauseAndSentenceCase checks that archiveErr keeps the cause
// for errors.Is, appends the cause text to the message, and starts the
// message with an upper case letter.
func TestArchiveErr_CauseAndSentenceCase(t *testing.T) {
	cause := errors.New("disk on fire")
	err := archiveErr(ArchiveFailed, "move x", archiveSuggestFSFailed, cause)
	if !errors.Is(err, cause) {
		t.Errorf("errors.Is(err, cause) = false, want true")
	}
	var ae *ArchiveError
	if !errors.As(err, &ae) || ae.Message != "Move x: disk on fire" || ae.Cause != cause {
		t.Errorf("error = %+v, want message %q and the cause", ae, "Move x: disk on fire")
	}
	if got := archiveErr(ArchiveBadRunID, "", archiveSuggestBadID, cause).(*ArchiveError).Message; got != "Disk on fire" {
		t.Errorf("message of a cause alone = %q, want %q", got, "Disk on fire")
	}
	if got := archiveErr(ArchiveRunNotFound, "no row", archiveSuggestNotFound, nil); errors.Unwrap(got) != nil || got.(*ArchiveError).Message != "No row" {
		t.Errorf("error without cause = %+v, want message %q and no cause", got, "No row")
	}
}
