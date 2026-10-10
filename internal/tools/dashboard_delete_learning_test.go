package tools

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
)

// Three learning entries and the log header that the delete tests write, oldest
// first.
const (
	learnDelHeader = "# SDLC Execution Learnings\n"
	learnDelOld    = "## 2026-10-08 — Pin schema hashes\nKeep the schema hash in the scaffold test.\n"
	learnDelMid    = "## 2026-10-09 — Use fsx for store writes\nWrite the store through a temp file and a rename.\n"
	learnDelNew    = "## 2026-10-10 — Cap the read size\nStat the file before the read.\n"
)

// learnDelLogDir returns the learnings folder of root and creates it.
func learnDelLogDir(t *testing.T, root string) string {
	t.Helper()
	dir := filepath.Dir(learningsLogPath(root))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// learnDelLog writes the learnings log of root: the header, then each entry
// after one blank line. It returns the path and the bytes written.
func learnDelLog(t *testing.T, root string, entries ...string) (string, []byte) {
	t.Helper()
	learnDelLogDir(t, root)
	content := learnDelHeader
	for _, e := range entries {
		content += "\n" + e
	}
	path := learningsLogPath(root)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path, []byte(content)
}

// learnDelWant checks that the log at path holds exactly want.
func learnDelWant(t *testing.T, path string, want []byte) {
	t.Helper()
	if got := dashDelReadFile(t, path); !bytes.Equal(got, want) {
		t.Errorf("learnings log =\n%q\nwant\n%q", got, want)
	}
}

// TestDashboardDeleteLearning_RemovesOnlyTheMatchedEntry removes only the matched entry; the header and the other entries stay byte-equal.
func TestDashboardDeleteLearning_RemovesOnlyTheMatchedEntry(t *testing.T) {
	root := t.TempDir()
	path, _ := learnDelLog(t, root, learnDelOld, learnDelMid, learnDelNew)

	out, err := DashboardDeleteLearning(root, "2026-10-09", "Use fsx for store writes")
	if err != nil {
		t.Fatalf("DashboardDeleteLearning: %v", err)
	}
	if !out.Deleted || out.AlreadyGone || out.Message == "" {
		t.Errorf("out = %+v, want Deleted with a message", out)
	}
	learnDelWant(t, path, []byte(learnDelHeader+"\n"+learnDelOld+"\n"+learnDelNew))
}

// TestDashboardDeleteLearning_RemovesNewestOfTwoWithSameKey removes only the newest of two entries with the same date and heading.
func TestDashboardDeleteLearning_RemovesNewestOfTwoWithSameKey(t *testing.T) {
	root := t.TempDir()
	first := "## 2026-10-09 — Retry the write\nfirst body\n"
	second := "## 2026-10-09 — Retry the write\nsecond body\n"
	path, _ := learnDelLog(t, root, learnDelOld, first, learnDelNew, second)

	out, err := DashboardDeleteLearning(root, "2026-10-09", "Retry the write")
	if err != nil {
		t.Fatalf("DashboardDeleteLearning: %v", err)
	}
	if !out.Deleted {
		t.Errorf("out = %+v, want Deleted", out)
	}
	// The newest match (the last in the log) goes. The older twin stays.
	learnDelWant(t, path, []byte(learnDelHeader+"\n"+learnDelOld+"\n"+first+"\n"+learnDelNew))
}

// TestDashboardDeleteLearning_LongHeadingMatchesSnapshotText matches a long heading by the cut text that the snapshot shows.
func TestDashboardDeleteLearning_LongHeadingMatchesSnapshotText(t *testing.T) {
	root := t.TempDir()
	long := "## 2026-10-09 — " + strings.Repeat("long heading ", 20) + "\nbody\n"
	path, _ := learnDelLog(t, root, learnDelOld, long)

	// The snapshot sends the cut heading, so the delete must match that text.
	heading := dashboardLearningHeading(strings.TrimSpace(long))
	if !strings.HasSuffix(heading, "…") {
		t.Fatalf("heading %q is not cut, the test needs a long heading", heading)
	}

	out, err := DashboardDeleteLearning(root, "2026-10-09", heading)
	if err != nil {
		t.Fatalf("DashboardDeleteLearning: %v", err)
	}
	if !out.Deleted {
		t.Errorf("out = %+v, want Deleted", out)
	}
	learnDelWant(t, path, []byte(learnDelHeader+"\n"+learnDelOld))
}

// TestDashboardDeleteLearning_LastEntryLeavesHeaderOnly leaves the header only when the last entry is removed.
func TestDashboardDeleteLearning_LastEntryLeavesHeaderOnly(t *testing.T) {
	root := t.TempDir()
	path, _ := learnDelLog(t, root, learnDelNew)

	out, err := DashboardDeleteLearning(root, "2026-10-10", "Cap the read size")
	if err != nil {
		t.Fatalf("DashboardDeleteLearning: %v", err)
	}
	if !out.Deleted {
		t.Errorf("out = %+v, want Deleted", out)
	}
	learnDelWant(t, path, []byte(learnDelHeader))
}

// TestDashboardDeleteLearning_AbsentLogIsAlreadyGone gives already gone when the log file is absent.
func TestDashboardDeleteLearning_AbsentLogIsAlreadyGone(t *testing.T) {
	root := t.TempDir()

	out, err := DashboardDeleteLearning(root, "2026-10-09", "Use fsx for store writes")
	if err != nil {
		t.Fatalf("DashboardDeleteLearning: %v", err)
	}
	if !out.AlreadyGone || out.Deleted || out.Message == "" {
		t.Errorf("out = %+v, want AlreadyGone with a message", out)
	}
	if _, err := os.Stat(learningsLogPath(root)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the delete created the log: %v", err)
	}
}

// TestDashboardDeleteLearning_UnmatchedPairIsAlreadyGone gives already gone when no entry matches the date and heading pair.
func TestDashboardDeleteLearning_UnmatchedPairIsAlreadyGone(t *testing.T) {
	cases := []struct{ name, date, heading string }{
		{"unknown pair", "2026-01-01", "No such learning"},
		{"date matches, heading differs", "2026-10-09", "Use fsx"},
		{"heading matches, date differs", "2026-10-08", "Use fsx for store writes"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			path, before := learnDelLog(t, root, learnDelOld, learnDelMid, learnDelNew)

			out, err := DashboardDeleteLearning(root, tc.date, tc.heading)
			if err != nil {
				t.Fatalf("DashboardDeleteLearning: %v", err)
			}
			if !out.AlreadyGone || out.Deleted {
				t.Errorf("out = %+v, want AlreadyGone", out)
			}
			if !strings.HasPrefix(out.Message, "No learning has the date ") {
				t.Errorf("message = %q, want the no-match message", out.Message)
			}
			learnDelWant(t, path, before)
		})
	}
}

// TestDashboardDeleteLearning_HeaderOnlyLogIsAlreadyGone gives already gone when the log holds only the header.
func TestDashboardDeleteLearning_HeaderOnlyLogIsAlreadyGone(t *testing.T) {
	root := t.TempDir()
	path, before := learnDelLog(t, root)

	out, err := DashboardDeleteLearning(root, "2026-10-09", "Use fsx for store writes")
	if err != nil {
		t.Fatalf("DashboardDeleteLearning: %v", err)
	}
	if !out.AlreadyGone {
		t.Errorf("out = %+v, want AlreadyGone", out)
	}
	if !strings.HasPrefix(out.Message, "The learnings log has no entries.") {
		t.Errorf("message = %q, want the empty-log message", out.Message)
	}
	learnDelWant(t, path, before)
}

// TestDashboardDeleteLearning_EmptyDateOrHeadingRefused refuses an empty date or heading with a DomainError.
func TestDashboardDeleteLearning_EmptyDateOrHeadingRefused(t *testing.T) {
	cases := []struct{ name, date, heading, message string }{
		{"empty date", "", "Use fsx for store writes", "The date field is required"},
		{"empty heading", "2026-10-09", "", "The heading field is required"},
		{"both empty", "", "", "The date and heading fields are required"},
		{"date not YYYY-MM-DD", "10/09/2026", "Use fsx for store writes", `The date "10/09/2026" is not a YYYY-MM-DD date`},
		{"date with extra text", "2026-10-09 x", "Use fsx for store writes", `The date "2026-10-09 x" is not a YYYY-MM-DD date`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			path, before := learnDelLog(t, root, learnDelMid)

			out, err := DashboardDeleteLearning(root, tc.date, tc.heading)
			dashDelWantErr(t, err, "domain", "Reload the page and try again.")
			if err.Error() != tc.message {
				t.Errorf("message = %q, want %q", err.Error(), tc.message)
			}
			if out != (DashboardDeleteOut{}) {
				t.Errorf("out = %+v, want zero value", out)
			}
			learnDelWant(t, path, before)
		})
	}
}

// TestDashboardDeleteLearning_TooLargeIsDataError gives a DataError for a log over the read limit and keeps the log bytes.
func TestDashboardDeleteLearning_TooLargeIsDataError(t *testing.T) {
	root := t.TempDir()
	// A valid log with one matching entry, padded past the limit: without the
	// size check the entry would be found and removed.
	learnDelLogDir(t, root)
	big := append([]byte(learnDelHeader+"\n"+learnDelMid+"\n"), bytes.Repeat([]byte(" "), dashboardDeleteReadMax)...)
	path := learningsLogPath(root)
	if err := os.WriteFile(path, big, 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := DashboardDeleteLearning(root, "2026-10-09", "Use fsx for store writes")
	dashDelWantErr(t, err, "data", "Remove old entries from .sdlc-v2/learnings/log.md by hand until it is smaller than 8 MiB, then retry.")
	want := fmt.Sprintf("The learnings log is %d bytes, more than the 8388608 bytes (8 MiB) that a dashboard delete reads", len(big))
	if err.Error() != want {
		t.Errorf("message = %q, want %q", err.Error(), want)
	}
	if out != (DashboardDeleteOut{}) {
		t.Errorf("out = %+v, want zero value", out)
	}
	learnDelWant(t, path, big)
}

// TestDashboardDeleteLearning_SizeAtLimitIsRead reads a log whose size equals the read limit.
func TestDashboardDeleteLearning_SizeAtLimitIsRead(t *testing.T) {
	root := t.TempDir()
	learnDelLogDir(t, root)
	// Exactly dashboardDeleteReadMax bytes is allowed.
	head := []byte(learnDelHeader + "\n" + learnDelMid)
	exact := append(head, bytes.Repeat([]byte(" "), dashboardDeleteReadMax-len(head))...)
	path := learningsLogPath(root)
	if err := os.WriteFile(path, exact, 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := DashboardDeleteLearning(root, "2026-10-09", "Use fsx for store writes")
	if err != nil {
		t.Fatalf("DashboardDeleteLearning: %v", err)
	}
	if !out.Deleted {
		t.Errorf("out = %+v, want Deleted", out)
	}
}

// TestDashboardDeleteLearning_LogIsFolderIsInfra gives an InfraError when the log path is a folder.
func TestDashboardDeleteLearning_LogIsFolderIsInfra(t *testing.T) {
	root := t.TempDir()
	// The log path is a folder: Stat succeeds and ReadFile fails.
	if err := os.MkdirAll(learningsLogPath(root), 0o755); err != nil {
		t.Fatal(err)
	}

	out, err := DashboardDeleteLearning(root, "2026-10-09", "Use fsx for store writes")
	dashDelWantErr(t, err, "infra", "Check that the learnings log is a regular file you can read, then retry.")
	if out != (DashboardDeleteOut{}) {
		t.Errorf("out = %+v, want zero value", out)
	}
	if info, statErr := os.Stat(learningsLogPath(root)); statErr != nil || !info.IsDir() {
		t.Errorf("the log folder changed: %v", statErr)
	}
}

// TestDashboardDeleteLearning_StatFailureIsInfra gives an InfraError when the log Stat fails.
func TestDashboardDeleteLearning_StatFailureIsInfra(t *testing.T) {
	root := t.TempDir()
	// The learnings folder is a plain file, so Stat of log.md gives ENOTDIR.
	if err := os.MkdirAll(filepath.Join(root, paths.DataDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Dir(learningsLogPath(root)), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := DashboardDeleteLearning(root, "2026-10-09", "Use fsx for store writes")
	dashDelWantErr(t, err, "infra", "Check that the learnings log is a regular file you can read, then retry.")
	if out != (DashboardDeleteOut{}) {
		t.Errorf("out = %+v, want zero value", out)
	}
}

// TestDashboardDeleteLearning_WriteFailureIsInfra gives an InfraError when the write fails and keeps the log bytes.
func TestDashboardDeleteLearning_WriteFailureIsInfra(t *testing.T) {
	root := t.TempDir()
	path, before := learnDelLog(t, root, learnDelOld, learnDelMid)
	injected := errors.New("injected write failure")
	dashDelSeam(t, &dashboardDeleteWriteBytes, func(string, []byte) error { return injected })

	out, err := DashboardDeleteLearning(root, "2026-10-09", "Use fsx for store writes")
	dashDelWantErr(t, err, "infra", "Check write permission on .sdlc-v2/learnings/ and free disk space, then retry.")
	if !errors.Is(err, injected) {
		t.Errorf("error does not wrap the injected failure: %v", err)
	}
	if out != (DashboardDeleteOut{}) {
		t.Errorf("out = %+v, want zero value", out)
	}
	learnDelWant(t, path, before)
}

// TestDashboardDeleteLearning_WritesThroughSeamWithRebuiltBytes checks that the write seam receives the log path and the rebuilt bytes.
func TestDashboardDeleteLearning_WritesThroughSeamWithRebuiltBytes(t *testing.T) {
	root := t.TempDir()
	path, _ := learnDelLog(t, root, learnDelOld, learnDelMid)
	var gotPath string
	var gotData []byte
	dashDelSeam(t, &dashboardDeleteWriteBytes, func(p string, data []byte) error {
		gotPath, gotData = p, data
		return nil
	})

	if _, err := DashboardDeleteLearning(root, "2026-10-08", "Pin schema hashes"); err != nil {
		t.Fatalf("DashboardDeleteLearning: %v", err)
	}
	if gotPath != path {
		t.Errorf("write path = %q, want %q", gotPath, path)
	}
	if want := learnDelHeader + "\n" + learnDelMid; string(gotData) != want {
		t.Errorf("write data = %q, want %q", gotData, want)
	}
}

// A row of the snapshot must be deletable with its own date and heading.
// TestDashboardDeleteLearning_DeletesEachSnapshotRow deletes each row that the snapshot lists, one row at a time.
func TestDashboardDeleteLearning_DeletesEachSnapshotRow(t *testing.T) {
	root := t.TempDir()
	path, _ := learnDelLog(t, root, learnDelOld, learnDelMid, learnDelNew)
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)

	rows := dashboardRecentLearnings(root, now)
	if len(rows) != 2 {
		t.Fatalf("snapshot rows = %d, want 2 (%+v)", len(rows), rows)
	}
	for _, row := range rows {
		out, err := DashboardDeleteLearning(root, row.Date, row.Heading)
		if err != nil {
			t.Fatalf("delete %+v: %v", row, err)
		}
		if !out.Deleted {
			t.Errorf("delete %+v: out = %+v, want Deleted", row, out)
		}
	}
	if left := dashboardRecentLearnings(root, now); len(left) != 0 {
		t.Errorf("rows left in the snapshot: %+v", left)
	}
	learnDelWant(t, path, []byte(learnDelHeader+"\n"+learnDelOld))
}

// learningsRemove and DashboardDeleteLearning share learningsJoin, so the same
// removal must give the same bytes.
// TestLearningsJoin_RemoveAndDashboardDeleteAgree checks that learningsRemove and the dashboard delete give the same bytes.
func TestLearningsJoin_RemoveAndDashboardDeleteAgree(t *testing.T) {
	viaTool := t.TempDir()
	viaDash := t.TempDir()
	toolPath, _ := learnDelLog(t, viaTool, learnDelOld, learnDelMid, learnDelNew)
	dashPath, _ := learnDelLog(t, viaDash, learnDelOld, learnDelMid, learnDelNew)

	if _, err := learningsLog(viaTool, LearningsLogIn{Action: "remove", Indices: []int{2}}); err != nil {
		t.Fatalf("learnings_log remove: %v", err)
	}
	if _, err := DashboardDeleteLearning(viaDash, "2026-10-09", "Use fsx for store writes"); err != nil {
		t.Fatalf("DashboardDeleteLearning: %v", err)
	}
	if a, b := dashDelReadFile(t, toolPath), dashDelReadFile(t, dashPath); !bytes.Equal(a, b) {
		t.Errorf("learnings_log remove gave %q, dashboard delete gave %q", a, b)
	}
}

// TestLearningsJoin checks the rebuild of a log from its header and kept entries.
func TestLearningsJoin(t *testing.T) {
	cases := []struct {
		name   string
		header string
		kept   []string
		want   string
	}{
		{"no entries", "# Log\n", nil, "# Log\n"},
		{"header without newline", "# Log", nil, "# Log\n"},
		{"header with extra newlines", "# Log\n\n\n", []string{"## a"}, "# Log\n\n## a\n"},
		{"two entries", "# Log", []string{"## a\nx", "## b"}, "# Log\n\n## a\nx\n\n## b\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := learningsJoin(tc.header, tc.kept); got != tc.want {
				t.Errorf("learningsJoin = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestDashboardDeleteLearning_LogGoneBeforeReadIsAlreadyGone gives already
// gone, not an InfraError, when the log goes away between the size check and
// the read.
func TestDashboardDeleteLearning_LogGoneBeforeReadIsAlreadyGone(t *testing.T) {
	root := t.TempDir()
	learnDelLog(t, root, learnDelMid)
	dashDelSeam(t, &dashboardDeleteReadFile, func(string) ([]byte, error) {
		return nil, &fs.PathError{Op: "open", Path: "log.md", Err: fs.ErrNotExist}
	})

	out, err := DashboardDeleteLearning(root, "2026-10-09", "Use fsx for store writes")
	if err != nil {
		t.Fatalf("DashboardDeleteLearning: %v", err)
	}
	if !out.AlreadyGone || out.Deleted {
		t.Errorf("out = %+v, want AlreadyGone", out)
	}
}
