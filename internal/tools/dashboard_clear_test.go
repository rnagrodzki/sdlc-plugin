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

// ccNow is the clock every ClearCache test passes in.
var ccNow = time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)

// ccEnv is one isolated ClearCache fixture. Every path is under t.TempDir.
// newCCEnv points clearTempDir at tmp, so a test can never reach the real
// temp directory. A test that swaps package vars must not run in parallel.
type ccEnv struct {
	t    *testing.T
	root string // repo root
	tmp  string // stands in for os.TempDir()
	log  string // stands in for the dashboard server.log
}

// newCCEnv creates the fixture and restores clearTempDir and clearRemove when
// the test ends.
func newCCEnv(t *testing.T) *ccEnv {
	t.Helper()
	base := t.TempDir()
	env := &ccEnv{
		t:    t,
		root: filepath.Join(base, "repo"),
		tmp:  filepath.Join(base, "tmp"),
		log:  filepath.Join(base, "cache", "dashboard", "server.log"),
	}
	for _, dir := range []string{env.root, env.tmp} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	origTemp, origRemove := clearTempDir, clearRemove
	clearTempDir = func() string { return env.tmp }
	t.Cleanup(func() { clearTempDir, clearRemove = origTemp, origRemove })
	return env
}

// data returns a path below <root>/.sdlc-v2.
func (e *ccEnv) data(parts ...string) string {
	return filepath.Join(append([]string{e.root, paths.DataDir}, parts...)...)
}

// clear runs ClearCache against the fixture and fails the test on an error.
func (e *ccEnv) clear() ClearCacheOut {
	e.t.Helper()
	out, err := ClearCache(e.root, e.log, ccNow)
	if err != nil {
		e.t.Fatalf("ClearCache: %v", err)
	}
	return out
}

// ccWrite writes a file of size bytes with the given age before ccNow.
func ccWrite(t *testing.T, path string, size int, age time.Duration) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(strings.Repeat("x", size)), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	if err := os.Chtimes(path, ccNow.Add(-age), ccNow.Add(-age)); err != nil {
		t.Fatalf("chtimes %s: %v", path, err)
	}
}

// ccWriteDir creates a directory that holds one file of size bytes, then sets
// the directory age. The age is set last, because writing the file changes it.
func ccWriteDir(t *testing.T, dir string, size int, age time.Duration) {
	t.Helper()
	ccWrite(t, filepath.Join(dir, "payload.txt"), size, age)
	if err := os.Chtimes(dir, ccNow.Add(-age), ccNow.Add(-age)); err != nil {
		t.Fatalf("chtimes %s: %v", dir, err)
	}
}

// ccState writes a state file of the given prefix whose startedAt is startedAt.
// ts is the timestamp part of the file name and keeps each name unique.
func ccState(t *testing.T, root, prefix, ts, startedAt string) {
	t.Helper()
	body := fmt.Sprintf(`{"startedAt":%q}`, startedAt)
	ccWriteRaw(t, filepath.Join(root, paths.DataDir, paths.RunsSubdir, prefix+"-main-"+ts+".json"), body)
}

// ccWriteRaw writes text to path and creates the parent directories.
func ccWriteRaw(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// ccExists reports whether path exists.
func ccExists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// ccClass returns the class row with the given name.
func ccClass(t *testing.T, out ClearCacheOut, name string) ClearCacheClass {
	t.Helper()
	for _, c := range out.Classes {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("class %q missing from %+v", name, out.Classes)
	return ClearCacheClass{}
}

// ccSkipReason returns the reason of the Skipped row for path, and whether a
// row exists.
func ccSkipReason(out ClearCacheOut, path string) (string, bool) {
	for _, s := range out.Skipped {
		if s.Path == path {
			return s.Reason, true
		}
	}
	return "", false
}

// ccWantSkip fails the test unless path has a Skipped row with the reason.
func ccWantSkip(t *testing.T, out ClearCacheOut, path, reason string) {
	t.Helper()
	got, ok := ccSkipReason(out, path)
	if !ok {
		t.Fatalf("no Skipped row for %s; rows: %+v", path, out.Skipped)
	}
	if got != reason {
		t.Errorf("Skipped reason for %s = %q, want %q", path, got, reason)
	}
}

// ccWantNoSkip fails the test when path has a Skipped row.
func ccWantNoSkip(t *testing.T, out ClearCacheOut, path string) {
	t.Helper()
	if reason, ok := ccSkipReason(out, path); ok {
		t.Errorf("unexpected Skipped row for %s: %q", path, reason)
	}
}

// ccWantClass fails the test unless the named class has the file count and bytes.
func ccWantClass(t *testing.T, out ClearCacheOut, name string, files int, bytes int64) {
	t.Helper()
	got := ccClass(t, out, name)
	if got.Files != files || got.Bytes != bytes {
		t.Errorf("class %s = {files:%d bytes:%d}, want {files:%d bytes:%d}", name, got.Files, got.Bytes, files, bytes)
	}
}

// ccSnapshot returns relative path -> "mtime|content" for every file below
// dir, except the file at skip.
func ccSnapshot(t *testing.T, dir, skip string) map[string]string {
	t.Helper()
	snap := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || p == skip {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		body, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		snap[rel] = fmt.Sprintf("%d|%s", info.ModTime().UnixNano(), body)
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot %s: %v", dir, err)
	}
	return snap
}

// TestClearCache_EvidenceRotations asserts that ClearCache deletes only the
// rotated evidence files older than the 30 minute gate. It keeps newer
// rotations, rotations at exactly the gate age, live evidence files, similar
// names and directories, and it reports the kept rotations as skipped.
func TestClearCache_EvidenceRotations(t *testing.T) {
	env := newCCEnv(t)
	evidence := func(name string) string { return env.data(paths.EvidenceSubdir, name) }

	// age against the 30 minute gate
	oldRot := evidence("cli-executions.jsonl.1")
	ccWrite(t, oldRot, 100, dashboardStallAfter+time.Second)
	newRot := evidence("mcp-invocations.jsonl.1")
	ccWrite(t, newRot, 50, dashboardStallAfter-time.Minute)
	edgeRot := evidence("user-inputs.jsonl.1")
	ccWrite(t, edgeRot, 10, dashboardStallAfter) // age equals the gate: kept
	// live evidence is never a target, however old
	live := evidence("cli-executions.jsonl")
	ccWrite(t, live, 70, 72*time.Hour)
	// a name that only looks similar stays
	other := evidence("notes.jsonl.1.bak")
	ccWrite(t, other, 5, 72*time.Hour)
	// a directory with a rotation-like name is not an evidence file
	dirRot := evidence("dir.jsonl.1")
	ccWriteDir(t, dirRot, 7, 72*time.Hour)

	out := env.clear()

	if ccExists(oldRot) {
		t.Errorf("old rotation %s must be deleted", oldRot)
	}
	for _, p := range []string{newRot, edgeRot, live, other, dirRot} {
		if !ccExists(p) {
			t.Errorf("%s must stay", p)
		}
	}
	ccWantClass(t, out, clearClassEvidence, 1, 100)
	ccWantSkip(t, out, newRot, "Changed less than 30 minutes ago")
	ccWantSkip(t, out, edgeRot, "Changed less than 30 minutes ago")
	ccWantNoSkip(t, out, live)
	ccWantNoSkip(t, out, other)
	ccWantNoSkip(t, out, dirRot)
	if out.FreedBytes != 100 {
		t.Errorf("FreedBytes = %d, want 100", out.FreedBytes)
	}
}

// TestClearCache_TempDirs asserts that ClearCache deletes only the sdlc-prefixed
// temp directories older than the 24 hour gate. It keeps newer directories,
// directories at exactly the gate age, the explore directories, foreign
// directories and plain files, and it reports the kept sdlc directories as
// skipped.
func TestClearCache_TempDirs(t *testing.T) {
	env := newCCEnv(t)
	tmp := func(name string) string { return filepath.Join(env.tmp, name) }

	oldDir := tmp("sdlc-review-old")
	ccWriteDir(t, oldDir, 40, clearTempDirMinAge+time.Second)
	freshDir := tmp("sdlc-harden-fresh")
	ccWriteDir(t, freshDir, 20, 23*time.Hour)
	edgeDir := tmp("sdlc-edge")
	ccWriteDir(t, edgeDir, 8, clearTempDirMinAge) // age equals the gate: kept
	exploreDir := tmp("sdlc-explore-main-abc123")
	ccWriteDir(t, exploreDir, 30, 100*time.Hour) // state.GC owns it
	foreignDir := tmp("other-tool-old")
	ccWriteDir(t, foreignDir, 30, 100*time.Hour)
	fileOnly := tmp("sdlc-commit-manifest-1.json")
	ccWrite(t, fileOnly, 9, 100*time.Hour) // a file is not a temp dir

	out := env.clear()

	if ccExists(oldDir) {
		t.Errorf("old temp dir %s must be deleted", oldDir)
	}
	for _, p := range []string{freshDir, edgeDir, exploreDir, foreignDir, fileOnly} {
		if !ccExists(p) {
			t.Errorf("%s must stay", p)
		}
	}
	ccWantClass(t, out, clearClassTempDirs, 1, 40)
	ccWantSkip(t, out, freshDir, "Changed less than 24 hours ago")
	ccWantSkip(t, out, edgeDir, "Changed less than 24 hours ago")
	for _, p := range []string{exploreDir, foreignDir, fileOnly} {
		ccWantNoSkip(t, out, p)
	}
}

// TestClearCache_OrphanReports asserts that ClearCache deletes only the report
// files whose run id matches no ship or execute run state (a plan run state
// owns no report). It keeps reports of existing runs, files with no run id in
// the name, partial temp files and directories, and it reports the kept files
// that have no run id as skipped.
func TestClearCache_OrphanReports(t *testing.T) {
	env := newCCEnv(t)
	report := func(name string) string { return env.data(paths.ReportsSubdir, name) }

	// run 20261009T081534 is a ship run, 20261008T120000 an execute run
	ccState(t, env.root, "ship", "20261009T081534Z", "2026-10-09T08:15:34Z")
	ccState(t, env.root, "execute", "20261008T120000Z", "2026-10-08T12:00:00Z")
	ccState(t, env.root, "plan", "20261007T100000Z", "2026-10-07T10:00:00Z")

	keepShip := report("ship-20261009T081534-report.md")
	keepExec := report("20261008T120000-report.json")
	orphanShip := report("ship-20260101T000000-report.md")
	orphanExec := report("20260102T000000-report.json")
	planOnly := report("20261007T100000-report.md") // a plan state owns no report
	unknown := report("notes.txt")
	tempFile := report("ship-20261009T081534-report.md.tmp-123")
	for path, size := range map[string]int{keepShip: 11, keepExec: 12, orphanShip: 13, orphanExec: 14, planOnly: 15, unknown: 16, tempFile: 17} {
		ccWrite(t, path, size, 0) // new age: no age gate applies to reports
	}
	if err := os.MkdirAll(report("sub-report.md"), 0o755); err != nil {
		t.Fatal(err)
	}

	out := env.clear()

	for _, p := range []string{orphanShip, orphanExec, planOnly} {
		if ccExists(p) {
			t.Errorf("orphan report %s must be deleted", p)
		}
	}
	for _, p := range []string{keepShip, keepExec, unknown, tempFile, report("sub-report.md")} {
		if !ccExists(p) {
			t.Errorf("%s must stay", p)
		}
	}
	ccWantClass(t, out, clearClassReports, 3, 13+14+15)
	ccWantSkip(t, out, unknown, "Report name has no run id")
	ccWantSkip(t, out, tempFile, "Report name has no run id")
	ccWantNoSkip(t, out, keepShip)
	ccWantNoSkip(t, out, keepExec)
	ccWantNoSkip(t, out, report("sub-report.md"))
}

// TestClearCache_OrphanReportsKeptWhenStatesUnreadable asserts that ClearCache
// deletes no report when one run state file cannot be parsed, because the run
// id of that state is unknown. It reports the reports directory as skipped and
// names the unreadable state.
func TestClearCache_OrphanReportsKeptWhenStatesUnreadable(t *testing.T) {
	env := newCCEnv(t)
	ccState(t, env.root, "ship", "20261009T081534Z", "2026-10-09T08:15:34Z")
	// a state file that does not parse: its run id is unknown
	ccWriteRaw(t, env.data(paths.RunsSubdir, "execute-main-20261009T090000Z.json"), "{not json")
	orphan := env.data(paths.ReportsSubdir, "ship-20260101T000000-report.md")
	ccWrite(t, orphan, 13, 0)

	out := env.clear()

	if !ccExists(orphan) {
		t.Errorf("report %s must stay when a run state is unreadable", orphan)
	}
	ccWantClass(t, out, clearClassReports, 0, 0)
	reason, ok := ccSkipReason(out, env.data(paths.ReportsSubdir))
	if !ok || !strings.Contains(reason, "1 run state files are unreadable") {
		t.Errorf("want a Skipped row for reports/ that names the unreadable state, got %q (found=%v)", reason, ok)
	}
}

// TestClearCache_ServerLogTruncatedAndStaysOpenForAppend asserts that
// ClearCache truncates the server log to zero bytes in place and counts the old
// bytes. A handle that the server holds open for append still writes at the
// start of the file, with no old bytes and no NUL hole.
func TestClearCache_ServerLogTruncatedAndStaysOpenForAppend(t *testing.T) {
	env := newCCEnv(t)
	if err := os.MkdirAll(filepath.Dir(env.log), 0o755); err != nil {
		t.Fatal(err)
	}
	// The server opens the log with O_APPEND (spawn_unix.go) and keeps it open.
	f, err := os.OpenFile(env.log, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString("first line\nsecond line\n"); err != nil {
		t.Fatal(err)
	}

	out := env.clear()

	ccWantClass(t, out, clearClassServerLog, 1, int64(len("first line\nsecond line\n")))
	info, err := os.Stat(env.log)
	if err != nil {
		t.Fatalf("server log must still exist: %v", err)
	}
	if info.Size() != 0 {
		t.Errorf("server log size = %d, want 0", info.Size())
	}
	if _, err := f.WriteString("after clear\n"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(env.log)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "after clear\n" {
		t.Errorf("log after a write through the open handle = %q, want %q (no old bytes, no NUL hole)", got, "after clear\n")
	}
}

// TestClearCache_ServerLogEdgeCases asserts that a missing log, an empty log and
// an empty log path each give an empty server-log class, and that a directory at
// the log path is not truncated and appears as a skipped row.
func TestClearCache_ServerLogEdgeCases(t *testing.T) {
	t.Run("missing log is an empty class", func(t *testing.T) {
		env := newCCEnv(t)
		out := env.clear()
		ccWantClass(t, out, clearClassServerLog, 0, 0)
		if len(out.Skipped) != 0 {
			t.Errorf("Skipped = %+v, want none", out.Skipped)
		}
	})
	t.Run("empty log is an empty class", func(t *testing.T) {
		env := newCCEnv(t)
		ccWriteRaw(t, env.log, "")
		out := env.clear()
		ccWantClass(t, out, clearClassServerLog, 0, 0)
	})
	t.Run("empty path is an empty class", func(t *testing.T) {
		env := newCCEnv(t)
		out, err := ClearCache(env.root, "", ccNow)
		if err != nil {
			t.Fatal(err)
		}
		ccWantClass(t, out, clearClassServerLog, 0, 0)
	})
	t.Run("a directory is not truncated", func(t *testing.T) {
		env := newCCEnv(t)
		if err := os.MkdirAll(env.log, 0o755); err != nil {
			t.Fatal(err)
		}
		out := env.clear()
		ccWantClass(t, out, clearClassServerLog, 0, 0)
		ccWantSkip(t, out, env.log, "Not a regular file")
	})
}

// TestClearCache_DeleteErrorAddsSkippedRowAndContinues asserts that a failed
// delete adds a skipped row with the error and leaves the path in place, while
// ClearCache still deletes the other paths of every class and counts only the
// bytes that it freed.
func TestClearCache_DeleteErrorAddsSkippedRowAndContinues(t *testing.T) {
	env := newCCEnv(t)
	failRot := env.data(paths.EvidenceSubdir, "a-fails.jsonl.1")
	okRot := env.data(paths.EvidenceSubdir, "b-works.jsonl.1")
	ccWrite(t, failRot, 10, time.Hour)
	ccWrite(t, okRot, 20, time.Hour)
	failTmp := filepath.Join(env.tmp, "sdlc-a-fails")
	okTmp := filepath.Join(env.tmp, "sdlc-b-works")
	ccWriteDir(t, failTmp, 30, 48*time.Hour)
	ccWriteDir(t, okTmp, 40, 48*time.Hour)
	failReport := env.data(paths.ReportsSubdir, "ship-20260101T000000-report.md")
	okReport := env.data(paths.ReportsSubdir, "ship-20260102T000000-report.md")
	ccWrite(t, failReport, 50, 0)
	ccWrite(t, okReport, 60, 0)

	clearRemove = func(path string) error {
		if strings.Contains(filepath.Base(path), "fails") || path == failReport {
			return errors.New("boom")
		}
		return os.RemoveAll(path)
	}

	out := env.clear()

	for _, p := range []string{failRot, failTmp, failReport} {
		if !ccExists(p) {
			t.Errorf("%s failed to delete and must stay", p)
		}
		ccWantSkip(t, out, p, "Delete failed: boom")
	}
	for _, p := range []string{okRot, okTmp, okReport} {
		if ccExists(p) {
			t.Errorf("%s must be deleted after the earlier error", p)
		}
	}
	ccWantClass(t, out, clearClassEvidence, 1, 20)
	ccWantClass(t, out, clearClassTempDirs, 1, 40)
	ccWantClass(t, out, clearClassReports, 1, 60)
	if out.FreedBytes != 120 {
		t.Errorf("FreedBytes = %d, want 120 (a failed delete frees nothing)", out.FreedBytes)
	}
}

// TestClearCache_EmptyResultShape asserts that ClearCache on an empty
// environment returns the four classes in the fixed order with zero files and
// zero bytes, and a non-nil empty skipped list. It also pins the JSON text of
// that result.
func TestClearCache_EmptyResultShape(t *testing.T) {
	env := newCCEnv(t)

	out := env.clear()

	var names []string
	for _, c := range out.Classes {
		names = append(names, c.Name)
		if c.Files != 0 || c.Bytes != 0 {
			t.Errorf("class %s = %+v, want files 0 and bytes 0", c.Name, c)
		}
	}
	want := []string{clearClassEvidence, clearClassTempDirs, clearClassReports, clearClassServerLog}
	if !reflect.DeepEqual(names, want) {
		t.Errorf("class order = %v, want %v", names, want)
	}
	if out.Skipped == nil {
		t.Error("Skipped is nil, want an empty slice")
	}
	raw, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	wantJSON := `{"freedBytes":0,"classes":[` +
		`{"name":"evidence-rotations","files":0,"bytes":0},` +
		`{"name":"temp-dirs","files":0,"bytes":0},` +
		`{"name":"orphan-reports","files":0,"bytes":0},` +
		`{"name":"server-log","files":0,"bytes":0}],"skipped":[]}`
	if string(raw) != wantJSON {
		t.Errorf("JSON = %s\nwant   %s", raw, wantJSON)
	}
}

// TestClearCache_KeepsEverythingElse asserts that ClearCache changes nothing
// outside its four classes. History, learnings, timings, the run archive, live
// evidence, current reports, config, and the cache binary and dashboard roots
// keep their content and modification time. Only the old rotation and the
// server log change.
func TestClearCache_KeepsEverythingElse(t *testing.T) {
	env := newCCEnv(t)
	cache := filepath.Dir(filepath.Dir(env.log)) // stands in for ~/.sdlc-cache
	old := 400 * time.Hour

	ccState(t, env.root, "ship", "20261009T081534Z", "2026-10-09T08:15:34Z")
	ccWrite(t, env.data(paths.HistorySubdir, "runs.jsonl"), 30, old)
	ccWrite(t, env.data(paths.LearningsSubdir, "learning-1.md"), 31, old)
	ccWrite(t, env.data(paths.TimingsFile), 32, old)
	ccWrite(t, env.data(paths.RunArchiveSubdir, "ship-x", "ship-x.json"), 33, old)
	ccWrite(t, env.data(paths.RunArchiveSubdir, "ship-x", "reports", "ship-20250101T000000-report.md"), 34, old)
	ccWrite(t, env.data(paths.EvidenceSubdir, "cli-executions.jsonl"), 35, old)
	ccWrite(t, env.data(paths.ReportsSubdir, "ship-20261009T081534-report.md"), 36, old)
	ccWrite(t, env.data(paths.ConfigFile), 37, old)
	ccWrite(t, filepath.Join(cache, "bin", "sdlc-mcp"), 38, old)
	ccWrite(t, filepath.Join(cache, "dashboard", "roots", "a.json"), 39, old)
	// a rotation that Clear deletes, so the test also proves that it ran
	ccWrite(t, env.data(paths.EvidenceSubdir, "cli-executions.jsonl.1"), 40, old)
	ccWrite(t, env.log, 41, 0)

	beforeRoot := ccSnapshot(t, env.root, "")
	beforeCache := ccSnapshot(t, cache, env.log)
	delete(beforeRoot, filepath.Join(paths.DataDir, paths.EvidenceSubdir, "cli-executions.jsonl.1"))

	out := env.clear()

	ccWantClass(t, out, clearClassEvidence, 1, 40)
	ccWantClass(t, out, clearClassServerLog, 1, 41)
	afterRoot := ccSnapshot(t, env.root, "")
	afterCache := ccSnapshot(t, cache, env.log)
	if !reflect.DeepEqual(beforeRoot, afterRoot) {
		t.Errorf("repo data changed beyond the rotation\nbefore: %v\nafter:  %v", beforeRoot, afterRoot)
	}
	if !reflect.DeepEqual(beforeCache, afterCache) {
		t.Errorf("cache data changed\nbefore: %v\nafter:  %v", beforeCache, afterCache)
	}
}

// TestClearCache_MissingDirsAreNotErrors asserts that ClearCache returns no
// error and no skipped row when the data directory of the root and the temp
// directory do not exist.
func TestClearCache_MissingDirsAreNotErrors(t *testing.T) {
	env := newCCEnv(t)
	// root has no .sdlc-v2, and the temp dir does not exist
	clearTempDir = func() string { return filepath.Join(env.tmp, "does-not-exist") }

	out, err := ClearCache(env.root, env.log, ccNow)
	if err != nil {
		t.Fatalf("ClearCache: %v", err)
	}
	if len(out.Skipped) != 0 {
		t.Errorf("Skipped = %+v, want none for missing directories", out.Skipped)
	}
}

// TestClearCache_UnreadableSubdirIsSkippedNotFatal asserts that a subdirectory
// that cannot be read as a directory gives a skipped row with a read-failed
// reason, and that ClearCache still continues with the next class.
func TestClearCache_UnreadableSubdirIsSkippedNotFatal(t *testing.T) {
	env := newCCEnv(t)
	// evidence is a file: reading it as a directory fails with an error other than not-exist
	evidence := env.data(paths.EvidenceSubdir)
	ccWriteRaw(t, evidence, "not a directory")
	oldTmp := filepath.Join(env.tmp, "sdlc-old")
	ccWriteDir(t, oldTmp, 5, 48*time.Hour)

	out := env.clear()

	reason, ok := ccSkipReason(out, evidence)
	if !ok || !strings.HasPrefix(reason, "Read failed: ") {
		t.Errorf("want a read-failed Skipped row for %s, got %q (found=%v)", evidence, reason, ok)
	}
	if ccExists(oldTmp) {
		t.Errorf("Clear must continue with the next class after a read error; %s stays", oldTmp)
	}
}

// TestClearCache_RootReadErrorIsInfraErrorWithSuggestion asserts that a root
// that is missing, empty or a plain file makes ClearCache return an InfraError
// with a Suggestion and a Cause, and that it deletes nothing.
func TestClearCache_RootReadErrorIsInfraErrorWithSuggestion(t *testing.T) {
	env := newCCEnv(t)
	oldTmp := filepath.Join(env.tmp, "sdlc-old")
	ccWriteDir(t, oldTmp, 5, 48*time.Hour)
	fileRoot := filepath.Join(env.tmp, "plain-file")
	ccWriteRaw(t, fileRoot, "x")

	cases := map[string]string{
		"missing root": filepath.Join(env.root, "gone"),
		"empty root":   "",
		"file root":    fileRoot,
	}
	for name, root := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := ClearCache(root, env.log, ccNow)
			var infra *mcpserver.InfraError
			if !errors.As(err, &infra) {
				t.Fatalf("error = %v (%T), want *mcpserver.InfraError", err, err)
			}
			if infra.Suggestion == "" {
				t.Error("InfraError.Suggestion is empty")
			}
			if infra.Cause == nil {
				t.Error("InfraError.Cause is nil")
			}
			if !ccExists(oldTmp) {
				t.Error("Clear must not delete anything when the root cannot be read")
			}
		})
	}
}

// TestClearCache_ClearTempDirDefaultsToOSTempDir asserts that the clearTempDir
// seam returns os.TempDir() when no test replaces it.
func TestClearCache_ClearTempDirDefaultsToOSTempDir(t *testing.T) {
	// The seam must default to the real temp dir; tests swap it, nothing else does.
	if got, want := clearTempDir(), os.TempDir(); got != want {
		t.Errorf("clearTempDir() = %q, want os.TempDir() %q", got, want)
	}
}

// TestClearTooNewReason asserts that clearTooNewReason words the skipped-row
// reason for the 30 minute gate and for the 24 hour gate.
func TestClearTooNewReason(t *testing.T) {
	cases := []struct {
		minAge time.Duration
		want   string
	}{
		{dashboardStallAfter, "Changed less than 30 minutes ago"},
		{clearTempDirMinAge, "Changed less than 24 hours ago"},
	}
	for _, tc := range cases {
		if got := clearTooNewReason(tc.minAge); got != tc.want {
			t.Errorf("clearTooNewReason(%v) = %q, want %q", tc.minAge, got, tc.want)
		}
	}
}

// ccSkipIfRoot skips a test whose fixture relies on a permission that root
// ignores.
func ccSkipIfRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root ignores file permissions")
	}
}

// ccChmod sets the mode of path and restores 0o755 when the test ends, so
// t.TempDir can remove the tree.
func ccChmod(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	if err := os.Chmod(path, mode); err != nil {
		t.Fatalf("chmod %s: %v", path, err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o755) })
}

// ccWantSkipPrefix fails the test unless out has a Skipped row for path whose
// reason starts with prefix and names path.
func ccWantSkipPrefix(t *testing.T, out ClearCacheOut, path, prefix string) {
	t.Helper()
	reason, ok := ccSkipReason(out, path)
	if !ok || !strings.HasPrefix(reason, prefix) || !strings.Contains(reason, path) {
		t.Errorf("Skipped row for %s = %q (found=%v), want a reason that starts with %q and names the path", path, reason, ok, prefix)
	}
}

// ccFakeEntry is a directory entry whose Info call returns err.
type ccFakeEntry struct {
	fs.DirEntry
	err error
}

// Info returns the error of the fake entry.
func (e ccFakeEntry) Info() (fs.FileInfo, error) { return nil, e.err }

// TestClearCache_ServerLogReadAndTruncateErrors asserts that a stat error and
// a truncate error of the server log each add a Skipped row with the error,
// leave the log as it was, and count nothing.
func TestClearCache_ServerLogReadAndTruncateErrors(t *testing.T) {
	t.Run("stat error", func(t *testing.T) {
		env := newCCEnv(t)
		// The parent of the log is a regular file, so stat fails with ENOTDIR.
		ccWriteRaw(t, filepath.Dir(env.log), "not a folder")
		out := env.clear()
		ccWantClass(t, out, clearClassServerLog, 0, 0)
		ccWantSkipPrefix(t, out, env.log, "Read failed: ")
		if out.FreedBytes != 0 {
			t.Errorf("FreedBytes = %d, want 0", out.FreedBytes)
		}
	})
	t.Run("truncate error", func(t *testing.T) {
		ccSkipIfRoot(t)
		env := newCCEnv(t)
		ccWrite(t, env.log, 41, 0)
		ccChmod(t, env.log, 0o444)
		out := env.clear()
		ccWantClass(t, out, clearClassServerLog, 0, 0)
		ccWantSkipPrefix(t, out, env.log, "Truncate failed: ")
		if info, err := os.Stat(env.log); err != nil || info.Size() != 41 {
			t.Errorf("server log after a failed truncate = %v, %v, want 41 bytes", info, err)
		}
	})
}

// TestClearCache_RemoveIfOlderInfoErrors asserts that removeIfOlder adds no
// row for an entry that left the directory, adds a read-failed row for any
// other Info error, and deletes and counts nothing in both cases.
func TestClearCache_RemoveIfOlderInfoErrors(t *testing.T) {
	removed := false
	orig := clearRemove
	clearRemove = func(string) error { removed = true; return nil }
	t.Cleanup(func() { clearRemove = orig })

	path := filepath.Join(t.TempDir(), "a.jsonl.1")
	for _, c := range []struct {
		name string
		err  error
		want []ClearCacheSkip
	}{
		{"entry is gone", fs.ErrNotExist, []ClearCacheSkip{}},
		{"other error", errors.New("boom"), []ClearCacheSkip{{Path: path, Reason: "Read failed: boom"}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := &clearSweep{now: ccNow, skipped: []ClearCacheSkip{}}
			class := ClearCacheClass{Name: clearClassEvidence}
			s.removeIfOlder(&class, path, ccFakeEntry{err: c.err}, dashboardStallAfter)
			if !reflect.DeepEqual(s.skipped, c.want) {
				t.Errorf("Skipped = %+v, want %+v", s.skipped, c.want)
			}
			if class.Files != 0 || class.Bytes != 0 || removed {
				t.Errorf("class = %+v, removed = %v, want nothing deleted", class, removed)
			}
		})
	}
}

// TestClearCache_TempDirSizeErrors asserts that a temp dir whose size walk
// fails stays in place with a read-failed row: once for a subdirectory that
// cannot be listed, once for a file whose info cannot be read.
func TestClearCache_TempDirSizeErrors(t *testing.T) {
	ccSkipIfRoot(t)
	for _, c := range []struct {
		name string
		mode os.FileMode
	}{
		{"subdirectory cannot be listed", 0o000},
		{"file info cannot be read", 0o444},
	} {
		t.Run(c.name, func(t *testing.T) {
			env := newCCEnv(t)
			dir := filepath.Join(env.tmp, "sdlc-walk")
			sub := filepath.Join(dir, "sub")
			ccWrite(t, filepath.Join(sub, "f.txt"), 7, 48*time.Hour)
			ccWriteDir(t, dir, 5, 48*time.Hour)
			ccChmod(t, sub, c.mode)

			out := env.clear()

			ccWantClass(t, out, clearClassTempDirs, 0, 0)
			ccWantSkipPrefix(t, out, dir, "Read failed: ")
			if !ccExists(filepath.Join(dir, "payload.txt")) {
				t.Errorf("%s must stay when its size cannot be read", dir)
			}
		})
	}
}

// TestClearCache_OrphanReportInfoErrors asserts the Info arms of
// orphanReports. A report that leaves reports/ after the listing adds no row.
// A report whose info cannot be read adds a read-failed row and stays. The
// clearRemove seam changes reports/ after the first delete.
func TestClearCache_OrphanReportInfoErrors(t *testing.T) {
	cases := []struct {
		name  string
		after func(t *testing.T, dir, second string)
		row   bool
	}{
		{"report is gone", func(t *testing.T, _, second string) {
			if err := os.Remove(second); err != nil {
				t.Fatal(err)
			}
		}, false},
		{"report info cannot be read", func(t *testing.T, dir, _ string) {
			ccChmod(t, dir, 0o644) // no search permission: lstat of a child fails
		}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.row {
				ccSkipIfRoot(t)
			}
			env := newCCEnv(t)
			dir := env.data(paths.ReportsSubdir)
			first := filepath.Join(dir, "ship-20260101T000000-report.md")
			second := filepath.Join(dir, "ship-20260102T000000-report.md")
			ccWrite(t, first, 10, 0)
			ccWrite(t, second, 20, 0)
			clearRemove = func(p string) error {
				if err := os.RemoveAll(p); err != nil {
					return err
				}
				if p == first {
					c.after(t, dir, second)
				}
				return nil
			}

			out := env.clear()

			ccWantClass(t, out, clearClassReports, 1, 10)
			if c.row {
				ccWantSkipPrefix(t, out, second, "Read failed: ")
				_ = os.Chmod(dir, 0o755)
				if !ccExists(second) {
					t.Errorf("%s must stay when its info cannot be read", second)
				}
			} else if len(out.Skipped) != 0 {
				t.Errorf("Skipped = %+v, want none for a report that is gone", out.Skipped)
			}
		})
	}
}

// TestClearCache_OrphanReportsKeptWhenRunsUnreadable asserts that a runs/
// that cannot be listed keeps every report and adds one read-failed row for
// reports/ that says why.
func TestClearCache_OrphanReportsKeptWhenRunsUnreadable(t *testing.T) {
	env := newCCEnv(t)
	ccWriteRaw(t, env.data(paths.RunsSubdir), "not a folder")
	orphan := env.data(paths.ReportsSubdir, "ship-20260101T000000-report.md")
	ccWrite(t, orphan, 10, 0)

	out := env.clear()

	if !ccExists(orphan) {
		t.Errorf("report %s must stay when runs/ cannot be listed", orphan)
	}
	ccWantClass(t, out, clearClassReports, 0, 0)
	reason, ok := ccSkipReason(out, env.data(paths.ReportsSubdir))
	if want := "Read failed: cannot list the run states, so no report is deleted: "; !ok || !strings.HasPrefix(reason, want) {
		t.Errorf("Skipped row for reports/ = %q (found=%v), want prefix %q", reason, ok, want)
	}
}
