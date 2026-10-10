package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/dashboard"
	"github.com/rnagrodzki/sdlc-plugin/internal/history"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
	"github.com/rnagrodzki/sdlc-plugin/internal/tools"
)

// Items that the end-to-end test writes to the temp repo. Each kind has one
// item that the test deletes and one item that must stay.
const (
	e2ePreplanGone = "e2e-topic"
	e2ePreplanKept = "e2e-kept-topic"

	e2eDeferredGone = "d-e2e-gone"
	e2eDeferredKept = "d-e2e-kept"

	e2eLearningDate = "2026-10-10"
	// Heading text of the learning that the test deletes and of the one that stays.
	e2eLearningGone = "E2E learning to delete"
	e2eLearningKept = "E2E learning to keep"
)

// e2eFixedNow is the clock of the test. The snapshot shows learnings of the
// last 24 hours only, so the learning entries carry the date of this time.
var e2eFixedNow = time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)

// e2eWriteFile writes data to path and creates the folder of the file.
func e2eWriteFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

// e2eSeedRepo writes two items of each kind to root: preplan topic files,
// deferred items and learning entries. It uses the same files that the
// snapshot reads, so no part of the read path is faked.
func e2eSeedRepo(t *testing.T, root string) {
	t.Helper()
	preplanDir := filepath.Join(root, paths.DataDir, paths.PreplanSubdir)
	// Statuses from the canonical list: "ready for plan" and "paused".
	e2eWriteFile(t, filepath.Join(preplanDir, e2ePreplanGone+".md"), "# Preplan: E2E topic\n\n**Status:** "+tools.PreplanStatuses[1]+"\n")
	e2eWriteFile(t, filepath.Join(preplanDir, e2ePreplanKept+".md"), "# Preplan: E2E kept topic\n\n**Status:** "+tools.PreplanStatuses[2]+"\n")

	items := []history.DeferredIssue{
		{ID: e2eDeferredGone, Created: "2026-10-09T08:00:00Z", Source: "review", Priority: history.PriorityHigh, Description: "E2E deferred item to delete", Status: history.StatusOpen},
		{ID: e2eDeferredKept, Created: "2026-10-09T09:00:00Z", Source: "review", Priority: history.PriorityLow, Description: "E2E deferred item to keep", Status: history.StatusOpen},
	}
	data, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	e2eWriteFile(t, history.NewFileWriter(paths.HistoryDir(root)).DeferredPath(), string(data)+"\n")

	log := "# SDLC Execution Learnings\n" +
		"\n## " + e2eLearningDate + " — " + e2eLearningKept + "\nThis entry stays.\n" +
		"\n## " + e2eLearningDate + " — " + e2eLearningGone + "\nThis entry goes.\n"
	e2eWriteFile(t, filepath.Join(root, paths.DataDir, paths.LearningsSubdir, "log.md"), log)
}

// e2eSnapshot sends GET /api/snapshot to h and returns the one repo with the
// given root.
func e2eSnapshot(t *testing.T, h http.Handler, root string) tools.DashboardRepo {
	t.Helper()
	rec := do(h, "GET", "/api/snapshot", testHost, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/snapshot status = %d; want 200 (body %q)", rec.Code, rec.Body.String())
	}
	var snap tools.DashboardSnapshot
	if err := json.Unmarshal(rec.Body.Bytes(), &snap); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	for _, repo := range snap.Repos {
		if repo.Root == root {
			if repo.Error != "" {
				t.Fatalf("repo error = %q; want none", repo.Error)
			}
			return repo
		}
	}
	t.Fatalf("snapshot has no repo with root %q: %+v", root, snap.Repos)
	return tools.DashboardRepo{}
}

// e2eHasPreplan reports whether repo lists the preplan topic with slug.
func e2eHasPreplan(repo tools.DashboardRepo, slug string) bool {
	for _, p := range repo.Preplans {
		if p.Slug == slug {
			return true
		}
	}
	return false
}

// e2eHasDeferred reports whether repo lists the deferred item with id.
func e2eHasDeferred(repo tools.DashboardRepo, id string) bool {
	for _, d := range repo.Deferred {
		if d.ID == id {
			return true
		}
	}
	return false
}

// e2eHasLearning reports whether repo lists the learning with date and heading.
func e2eHasLearning(repo tools.DashboardRepo, date, heading string) bool {
	for _, l := range repo.Learnings {
		if l.Date == date && l.Heading == heading {
			return true
		}
	}
	return false
}

// e2eDelete sends one delete request with the mutation headers and returns the
// decoded 200 body.
func e2eDelete(t *testing.T, h http.Handler, path string, body map[string]string) tools.DashboardDeleteOut {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	rec := postJSON(h, path, string(raw), mutationHeader())
	if rec.Code != http.StatusOK {
		t.Fatalf("POST %s status = %d; want 200 (body %q)", path, rec.Code, rec.Body.String())
	}
	var out tools.DashboardDeleteOut
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("POST %s: decode body %q: %v", path, rec.Body.String(), err)
	}
	return out
}

// TestE2E_DeleteEachKind runs the three delete routes against the real
// snapshot collector and the real delete functions on a temp repo. For each
// kind it checks: the item is in the snapshot, the delete answers deleted,
// the next snapshot has no item, and a second delete answers alreadyGone. An
// item of the same kind that the test does not delete stays in the snapshot.
func TestE2E_DeleteEachKind(t *testing.T) {
	oldNow := now
	now = func() time.Time { return e2eFixedNow }
	t.Cleanup(func() { now = oldNow })

	repoRoot := t.TempDir()
	e2eSeedRepo(t, repoRoot)

	o := testOptions(t, &fakeCollector{})
	o.Roots = func(time.Time) ([]dashboard.Root, error) {
		return []dashboard.Root{{Root: repoRoot, LastSeen: e2eFixedNow}}, nil
	}
	o.Collect = func(roots []string, at time.Time) tools.DashboardSnapshot {
		return tools.CollectDashboardSnapshot(roots, at, "test")
	}
	o.DeletePreplan = tools.DashboardDeletePreplan
	o.DeleteDeferred = tools.DashboardDeleteDeferred
	o.DeleteLearning = tools.DashboardDeleteLearning
	h := newHandler(context.Background(), o, testToken, 4242, e2eFixedNow, func() {})

	// Step 1: each item is in the first snapshot.
	before := e2eSnapshot(t, h, repoRoot)
	if !e2eHasPreplan(before, e2ePreplanGone) || !e2eHasPreplan(before, e2ePreplanKept) {
		t.Fatalf("before: preplans = %+v; want %q and %q", before.Preplans, e2ePreplanGone, e2ePreplanKept)
	}
	if !e2eHasDeferred(before, e2eDeferredGone) || !e2eHasDeferred(before, e2eDeferredKept) {
		t.Fatalf("before: deferred = %+v; want %q and %q", before.Deferred, e2eDeferredGone, e2eDeferredKept)
	}
	if !e2eHasLearning(before, e2eLearningDate, e2eLearningGone) || !e2eHasLearning(before, e2eLearningDate, e2eLearningKept) {
		t.Fatalf("before: learnings = %+v; want %q and %q", before.Learnings, e2eLearningGone, e2eLearningKept)
	}

	kinds := []struct {
		name string
		path string
		body map[string]string
	}{
		{"preplan", "/api/preplan-delete", map[string]string{"repo": repoRoot, "slug": e2ePreplanGone}},
		{"deferred", "/api/deferred-delete", map[string]string{"repo": repoRoot, "id": e2eDeferredGone}},
		{"learning", "/api/learning-delete", map[string]string{"repo": repoRoot, "date": e2eLearningDate, "heading": e2eLearningGone}},
	}

	// Step 2: each first delete answers 200 with deleted true.
	for _, k := range kinds {
		out := e2eDelete(t, h, k.path, k.body)
		if !out.Deleted || out.AlreadyGone || out.Message == "" {
			t.Errorf("%s delete: out = %+v; want deleted:true, alreadyGone:false and a message", k.name, out)
		}
	}

	// Step 3: the next snapshot has no deleted item, and the other items stay.
	after := e2eSnapshot(t, h, repoRoot)
	if e2eHasPreplan(after, e2ePreplanGone) || !e2eHasPreplan(after, e2ePreplanKept) {
		t.Errorf("after: preplans = %+v; want only %q", after.Preplans, e2ePreplanKept)
	}
	if e2eHasDeferred(after, e2eDeferredGone) || !e2eHasDeferred(after, e2eDeferredKept) {
		t.Errorf("after: deferred = %+v; want only %q", after.Deferred, e2eDeferredKept)
	}
	if e2eHasLearning(after, e2eLearningDate, e2eLearningGone) || !e2eHasLearning(after, e2eLearningDate, e2eLearningKept) {
		t.Errorf("after: learnings = %+v; want only %q", after.Learnings, e2eLearningKept)
	}
	gonePath := filepath.Join(repoRoot, paths.DataDir, paths.PreplanSubdir, e2ePreplanGone+".md")
	if _, err := os.Stat(gonePath); !os.IsNotExist(err) {
		t.Errorf("preplan file %s: Stat error = %v; want not exist", gonePath, err)
	}

	// Step 4: each second delete answers 200 with alreadyGone true.
	for _, k := range kinds {
		out := e2eDelete(t, h, k.path, k.body)
		if out.Deleted || !out.AlreadyGone || out.Message == "" {
			t.Errorf("%s second delete: out = %+v; want deleted:false, alreadyGone:true and a message", k.name, out)
		}
	}
}

// TestE2E_DeleteRefusesEmptyKey sends each delete route a body with an empty
// or bad key to the real delete functions. The route does not check the key,
// so the 400 and its message come from the delete function, and no file
// changes.
func TestE2E_DeleteRefusesEmptyKey(t *testing.T) {
	repoRoot := t.TempDir()
	e2eSeedRepo(t, repoRoot)

	o := testOptions(t, &fakeCollector{})
	o.Roots = func(time.Time) ([]dashboard.Root, error) {
		return []dashboard.Root{{Root: repoRoot, LastSeen: e2eFixedNow}}, nil
	}
	o.DeletePreplan = tools.DashboardDeletePreplan
	o.DeleteDeferred = tools.DashboardDeleteDeferred
	o.DeleteLearning = tools.DashboardDeleteLearning
	h := newHandler(context.Background(), o, testToken, 4242, e2eFixedNow, func() {})

	cases := []struct {
		name, path, body, message string
	}{
		{"empty slug", "/api/preplan-delete", `{"repo":%q,"slug":""}`, "The slug field is required"},
		{"slug with .md", "/api/preplan-delete", `{"repo":%q,"slug":"` + e2ePreplanGone + `.md"}`,
			`The slug "` + e2ePreplanGone + `.md" ends in .md. Send the file name without .md`},
		{"empty id", "/api/deferred-delete", `{"repo":%q,"id":""}`, "The id field is required"},
		{"empty date", "/api/learning-delete", `{"repo":%q,"date":"","heading":"h"}`, "The date field is required"},
		{"empty heading", "/api/learning-delete", `{"repo":%q,"date":"2026-10-10","heading":""}`, "The heading field is required"},
		{"bad date", "/api/learning-delete", `{"repo":%q,"date":"10/10/2026","heading":"h"}`, `The date "10/10/2026" is not a YYYY-MM-DD date`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := postJSON(h, tc.path, fmt.Sprintf(tc.body, repoRoot), mutationHeader())
			got := wantAPIError(t, rec, http.StatusBadRequest, codeBadRequest)
			if got.Error.Message != tc.message {
				t.Errorf("message = %q; want %q", got.Error.Message, tc.message)
			}
		})
	}

	after := e2eSnapshotRepo(t, repoRoot)
	if !e2eHasPreplan(after, e2ePreplanGone) || !e2eHasDeferred(after, e2eDeferredGone) || !e2eHasLearning(after, e2eLearningDate, e2eLearningGone) {
		t.Errorf("a refused delete changed a file: %+v", after)
	}
}

// e2eSnapshotRepo collects the snapshot of root at e2eFixedNow without the
// HTTP route.
func e2eSnapshotRepo(t *testing.T, root string) tools.DashboardRepo {
	t.Helper()
	snap := tools.CollectDashboardSnapshot([]string{root}, e2eFixedNow, "test")
	if len(snap.Repos) != 1 {
		t.Fatalf("snapshot repos = %d; want 1", len(snap.Repos))
	}
	return snap.Repos[0]
}
