package tools

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
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
	if err := os.Remove(block); err != nil {
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
