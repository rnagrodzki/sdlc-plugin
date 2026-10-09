package tools

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
)

// reviewPlanTestRun is the ledger run folder name of the reader tests.
const reviewPlanTestRun = "review-2026-10-07T09-00-00Z"

// reviewPlanWriteMeta writes run.meta of ledger run folder run with mtime.
func reviewPlanWriteMeta(t *testing.T, root, run string, meta reviewRunMeta, mtime time.Time) {
	t.Helper()
	dashWriteJSON(t, ledgerRunMetaPath(root, run), meta, mtime)
}

// reviewPlanPublic returns rows with the unexported fields cleared, so a test
// can compare the public row shape with reflect.DeepEqual.
func reviewPlanPublic(rows []reviewPlanRow) []reviewPlanRow {
	out := make([]reviewPlanRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, reviewPlanRow{
			Wave: r.Wave, Name: r.Name, WorkerID: r.WorkerID, Status: r.Status, Reason: r.Reason,
			Findings: r.Findings, Worst: r.Worst, DurationSec: r.DurationSec,
		})
	}
	return out
}

// TestReadReviewLedgerPlan_PlannedRows checks every arm of the status table,
// the wave of each row, the totals, that an unplanned worker file is not a
// row, the finding count and worst severity, the duration, and that updated is
// the newest modification time of run.meta and the worker files.
func TestReadReviewLedgerPlan_PlannedRows(t *testing.T) {
	root := dashRoot(t)
	metaTime := dashNow.Add(-40 * time.Minute)
	fileTime := dashNow.Add(-5 * time.Minute)
	reviewPlanWriteMeta(t, root, reviewPlanTestRun, reviewRunMeta{
		Branch: "feat/x", StartedAt: "2026-10-07T09:00:00Z",
		Waves: [][]string{{"security", "docs", "late"}, {"perf", "lost"}, {"never"}},
		Dimensions: []reviewRunMetaDimension{
			{Name: "Security", WorkerID: "security", Wave: 1},
			{Name: "Docs", WorkerID: "docs", Wave: 1, StopReason: reviewStopStalled},
			{Name: "Late", WorkerID: "late", Wave: 1, StopReason: reviewStopUnstopped},
			{Name: "Perf", WorkerID: "perf", Wave: 2},
			{Name: "Lost", WorkerID: "lost", Wave: 2, StopReason: reviewStopMissing},
			{Name: "Never", WorkerID: "never", Wave: 3},
		},
	}, metaTime)
	dashWriteReviewDim(t, root, reviewPlanTestRun, "security", map[string]any{
		"checkinAt": "2026-10-07T09:00:00Z", "checkoutAt": "2026-10-07T09:04:12Z",
		"findings": dashLedgerFindings(t,
			map[string]any{"severity": "warning", "file": "a.go", "rationale": "a"},
			map[string]any{"severity": "high", "file": "b.go", "rationale": "b"},
		),
	}, metaTime)
	dashWriteReviewDim(t, root, reviewPlanTestRun, "docs", map[string]any{"checkinAt": "2026-10-07T09:00:00Z"}, metaTime)
	dashWriteReviewDim(t, root, reviewPlanTestRun, "late", map[string]any{
		"checkinAt": "2026-10-07T09:00:00Z", "checkoutAt": "2026-10-07T09:10:00Z",
	}, metaTime)
	dashWriteReviewDim(t, root, reviewPlanTestRun, "perf", map[string]any{"checkinAt": "2026-10-07T09:20:00Z"}, fileTime)
	dashWriteReviewDim(t, root, reviewPlanTestRun, "extra", map[string]any{"checkinAt": "2026-10-07T09:20:00Z"}, metaTime)

	rows, totals, updated, err := readReviewLedgerPlan(root, reviewPlanTestRun)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	want := []reviewPlanRow{
		{Wave: 1, Name: "Security", WorkerID: "security", Status: StepCompleted, Findings: 2, Worst: "high", DurationSec: 252},
		{Wave: 1, Name: "Docs", WorkerID: "docs", Status: StepSkipped, Reason: reviewStopStalled},
		{Wave: 1, Name: "Late", WorkerID: "late", Status: StepCompleted, DurationSec: 600},
		{Wave: 2, Name: "Perf", WorkerID: "perf", Status: StepInProgress},
		{Wave: 2, Name: "Lost", WorkerID: "lost", Status: StepSkipped, Reason: reviewStopMissing},
		{Wave: 3, Name: "Never", WorkerID: "never", Status: StepPending},
	}
	if got := reviewPlanPublic(rows); !reflect.DeepEqual(got, want) {
		t.Errorf("rows =\n%+v\nwant\n%+v", got, want)
	}
	wantTotals := reviewPlanTotals{WavesPlanned: 3, WavesRun: 2, DimensionsPlanned: 6, DimensionsRun: 4, NeverStarted: 2}
	if totals != wantTotals {
		t.Errorf("totals = %+v, want %+v", totals, wantTotals)
	}
	if !updated.Equal(fileTime) {
		t.Errorf("updated = %v, want the newest file time %v", updated, fileTime)
	}
	steps := []string{StepPending, StepInProgress, StepCompleted, StepSkipped}
	for _, r := range rows {
		if !slices.Contains(steps, r.Status) {
			t.Errorf("row %q status %q is not a step constant", r.Name, r.Status)
		}
	}
}

// TestReadReviewLedgerPlan_NoPlan checks that a run.meta without dimensions,
// a run.meta that does not parse, and a folder with no run.meta each give one
// wave-0 row per worker file in file name order, zero totals, and no error.
func TestReadReviewLedgerPlan_NoPlan(t *testing.T) {
	cases := []struct {
		name  string
		write func(t *testing.T, root string)
	}{
		{"run.meta without dimensions", func(t *testing.T, root string) {
			reviewPlanWriteMeta(t, root, reviewPlanTestRun, reviewRunMeta{Branch: "feat/x", StartedAt: "2026-10-07T09:00:00Z"}, dashNow)
		}},
		{"run.meta does not parse", func(t *testing.T, root string) {
			if err := os.WriteFile(ledgerRunMetaPath(root, reviewPlanTestRun), []byte("{not json"), 0o644); err != nil {
				t.Fatal(err)
			}
		}},
		{"no run.meta", func(*testing.T, string) {}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := dashRoot(t)
			dashWriteReviewDim(t, root, reviewPlanTestRun, "security", map[string]any{
				"checkinAt": "2026-10-07T09:00:00Z", "checkoutAt": "2026-10-07T09:01:00Z",
			}, dashNow)
			dashWriteReviewDim(t, root, reviewPlanTestRun, "docs", map[string]any{"checkinAt": "2026-10-07T09:00:00Z"}, dashNow)
			tc.write(t, root)

			rows, totals, _, err := readReviewLedgerPlan(root, reviewPlanTestRun)
			if err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			want := []reviewPlanRow{
				{Name: "docs", WorkerID: "docs", Status: StepInProgress},
				{Name: "security", WorkerID: "security", Status: StepCompleted, DurationSec: 60},
			}
			if got := reviewPlanPublic(rows); !reflect.DeepEqual(got, want) {
				t.Errorf("rows = %+v, want %+v", got, want)
			}
			if totals != (reviewPlanTotals{}) {
				t.Errorf("totals = %+v, want zero", totals)
			}
		})
	}
}

// TestReadReviewLedgerPlan_OnlyRunMeta checks that a folder with only a
// planned run.meta gives pending rows and takes the run.meta modification
// time as updated.
func TestReadReviewLedgerPlan_OnlyRunMeta(t *testing.T) {
	root := dashRoot(t)
	metaTime := dashNow.Add(-31 * time.Minute)
	reviewPlanWriteMeta(t, root, reviewPlanTestRun, reviewRunMeta{
		Waves:      [][]string{{"docs"}},
		Dimensions: []reviewRunMetaDimension{{Name: "docs", WorkerID: "docs", Wave: 1}},
	}, metaTime)

	rows, totals, updated, err := readReviewLedgerPlan(root, reviewPlanTestRun)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if want := []reviewPlanRow{{Wave: 1, Name: "docs", WorkerID: "docs", Status: StepPending}}; !reflect.DeepEqual(reviewPlanPublic(rows), want) {
		t.Errorf("rows = %+v, want %+v", rows, want)
	}
	if want := (reviewPlanTotals{WavesPlanned: 1, DimensionsPlanned: 1, NeverStarted: 1}); totals != want {
		t.Errorf("totals = %+v, want %+v", totals, want)
	}
	if !updated.Equal(metaTime) {
		t.Errorf("updated = %v, want the run.meta time %v", updated, metaTime)
	}
}

// TestReadReviewLedgerPlan_SkipsBadWorkerFile checks that a worker file that
// does not parse, and a worker file that cannot be read (a dangling link),
// give no row and leave the planned dimension pending.
func TestReadReviewLedgerPlan_SkipsBadWorkerFile(t *testing.T) {
	root := dashRoot(t)
	reviewPlanWriteMeta(t, root, reviewPlanTestRun, reviewRunMeta{
		Dimensions: []reviewRunMetaDimension{{Name: "docs", WorkerID: "docs", Wave: 1}},
	}, dashNow)
	if err := os.WriteFile(ledgerFilePath(root, reviewPlanTestRun, "docs"), []byte("{bad"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "nonexistent"), ledgerFilePath(root, reviewPlanTestRun, "ghost")); err != nil {
		t.Fatal(err)
	}
	rows, totals, _, err := readReviewLedgerPlan(root, reviewPlanTestRun)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if len(rows) != 1 || rows[0].Status != StepPending || totals.DimensionsRun != 0 {
		t.Errorf("rows = %+v, totals = %+v, want one pending row and no run dimension", rows, totals)
	}
}

// TestReadReviewLedgerPlan_SkipsSubfolders checks that a subfolder of the run
// folder, whether its name ends in .json or not, adds no row and does not
// change updated, and that a worker file inside a subfolder is not read.
func TestReadReviewLedgerPlan_SkipsSubfolders(t *testing.T) {
	root := dashRoot(t)
	fileTime := dashNow.Add(-20 * time.Minute)
	newer := dashNow.Add(-1 * time.Minute)
	dashWriteReviewDim(t, root, reviewPlanTestRun, "security", map[string]any{"checkinAt": "2026-10-07T09:00:00Z"}, fileTime)
	// A folder named like a worker file, with a worker file inside it.
	dashWriteReviewDim(t, root, reviewPlanTestRun, "ghost.json/inner", map[string]any{"checkinAt": "2026-10-07T09:00:00Z"}, newer)
	// A folder whose name does not end in .json, with a worker file inside it.
	dashWriteReviewDim(t, root, reviewPlanTestRun, "nested/inner", map[string]any{"checkinAt": "2026-10-07T09:00:00Z"}, newer)
	for _, sub := range []string{"ghost.json", "nested"} {
		if err := os.Chtimes(filepath.Join(ledgerDir(root, reviewPlanTestRun), sub), newer, newer); err != nil {
			t.Fatal(err)
		}
	}

	rows, totals, updated, err := readReviewLedgerPlan(root, reviewPlanTestRun)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	want := []reviewPlanRow{{Name: "security", WorkerID: "security", Status: StepInProgress}}
	if got := reviewPlanPublic(rows); !reflect.DeepEqual(got, want) {
		t.Errorf("rows = %+v, want %+v", got, want)
	}
	if totals != (reviewPlanTotals{}) {
		t.Errorf("totals = %+v, want zero", totals)
	}
	if !updated.Equal(fileTime) {
		t.Errorf("updated = %v, want the worker file time %v", updated, fileTime)
	}
}

// TestReviewWorkerRow_Duration checks the duration rule and the status of a
// worker file: the duration is the whole seconds from checkinAt to checkoutAt,
// and it is 0 when checkoutAt is not after checkinAt or when one of the two
// times is missing or does not parse. The status is completed when the file has
// a checkoutAt key, whether or not its value parses, else in progress.
func TestReviewWorkerRow_Duration(t *testing.T) {
	cases := []struct {
		name       string
		data       map[string]any
		wantSec    int
		wantStatus string
	}{
		{"valid pair", map[string]any{"checkinAt": "2026-10-07T09:00:00Z", "checkoutAt": "2026-10-07T09:04:12Z"}, 252, StepCompleted},
		{"valid pair with fractions", map[string]any{"checkinAt": "2026-10-07T09:00:00.2Z", "checkoutAt": "2026-10-07T09:00:03.9Z"}, 3, StepCompleted},
		{"checkoutAt equals checkinAt", map[string]any{"checkinAt": "2026-10-07T09:00:00Z", "checkoutAt": "2026-10-07T09:00:00Z"}, 0, StepCompleted},
		{"checkoutAt before checkinAt", map[string]any{"checkinAt": "2026-10-07T09:05:00Z", "checkoutAt": "2026-10-07T09:00:00Z"}, 0, StepCompleted},
		{"missing checkinAt", map[string]any{"checkoutAt": "2026-10-07T09:04:12Z"}, 0, StepCompleted},
		{"unparseable checkinAt", map[string]any{"checkinAt": "soon", "checkoutAt": "2026-10-07T09:04:12Z"}, 0, StepCompleted},
		{"unparseable checkoutAt", map[string]any{"checkinAt": "2026-10-07T09:00:00Z", "checkoutAt": "later"}, 0, StepCompleted},
		{"missing checkoutAt", map[string]any{"checkinAt": "2026-10-07T09:00:00Z"}, 0, StepInProgress},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "security.json")
			dashWriteJSON(t, path, tc.data, dashNow)
			row, ok := reviewWorkerRow(path, "security")
			if !ok {
				t.Fatal("ok = false, want true")
			}
			if row.DurationSec != tc.wantSec || row.Status != tc.wantStatus {
				t.Errorf("DurationSec = %d, Status = %q, want %d, %q", row.DurationSec, row.Status, tc.wantSec, tc.wantStatus)
			}
		})
	}
}

// TestReadReviewLedgerPlan_FolderErrors checks that a missing folder gives
// no rows and no error, and that a folder path that is not a folder gives an
// error and no rows.
func TestReadReviewLedgerPlan_FolderErrors(t *testing.T) {
	t.Run("missing folder", func(t *testing.T) {
		rows, totals, updated, err := readReviewLedgerPlan(dashRoot(t), reviewPlanTestRun)
		if err != nil || len(rows) != 0 || totals != (reviewPlanTotals{}) || !updated.IsZero() {
			t.Errorf("got rows=%v totals=%+v updated=%v err=%v, want all empty", rows, totals, updated, err)
		}
	})
	t.Run("path is a file", func(t *testing.T) {
		root := dashRoot(t)
		path := ledgerDir(root, reviewPlanTestRun)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		rows, _, _, err := readReviewLedgerPlan(root, reviewPlanTestRun)
		if err == nil || len(rows) != 0 {
			t.Errorf("rows = %v, err = %v, want an error and no rows", rows, err)
		}
	})
}

// TestReviewPlanStatus checks every arm of the status rule, including that a
// checked-out worker is completed even with a stopReason.
func TestReviewPlanStatus(t *testing.T) {
	cases := []struct {
		name       string
		row        reviewPlanRow
		stop       string
		wantStatus string
		wantReason string
	}{
		{"no file, no reason", reviewPlanRow{}, "", StepPending, ""},
		{"no file, reason", reviewPlanRow{}, reviewStopMissing, StepSkipped, reviewStopMissing},
		{"active file, reason", reviewPlanRow{hasFile: true, Status: StepInProgress}, reviewStopStalled, StepSkipped, reviewStopStalled},
		{"active file, no reason", reviewPlanRow{hasFile: true, Status: StepInProgress}, "", StepInProgress, ""},
		{"checked out, reason", reviewPlanRow{hasFile: true, Status: StepCompleted}, reviewStopUnstopped, StepCompleted, ""},
		{"checked out, no reason", reviewPlanRow{hasFile: true, Status: StepCompleted}, "", StepCompleted, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, reason := reviewPlanStatus(tc.row, tc.stop)
			if status != tc.wantStatus || reason != tc.wantReason {
				t.Errorf("got (%q, %q), want (%q, %q)", status, reason, tc.wantStatus, tc.wantReason)
			}
		})
	}
}

// TestFindReviewLedgerByShipRun checks the match rules: the newest startedAt
// of the matching folders wins whatever the folder order, an equal startedAt
// picks the larger folder name, a startedAt that does not parse loses to one
// that parses, two startedAt values that do not parse pick the larger folder
// name, folders of another ship run, folders with a bad or missing run.meta,
// and entries that are not folders are skipped, and an empty id, a missing
// ledger folder, or no match give "" with no error.
func TestFindReviewLedgerByShipRun(t *testing.T) {
	const ship = "ship-feat-x-20261007T080000Z"
	writeMeta := func(t *testing.T, root, run, shipRunID, startedAt string) {
		t.Helper()
		reviewPlanWriteMeta(t, root, run, reviewRunMeta{StartedAt: startedAt, ShipRunID: shipRunID}, dashNow)
	}
	ledgerRoot := func(root string) string {
		return filepath.Join(root, paths.DataDir, paths.RunsSubdir, "ledger")
	}

	t.Run("a newer startedAt replaces the folder read first", func(t *testing.T) {
		root := dashRoot(t)
		// os.ReadDir visits review-a first, so review-b must replace it.
		writeMeta(t, root, "review-a", ship, "2026-10-07T09:00:00Z")
		writeMeta(t, root, "review-b", ship, "2026-10-07T09:30:00Z")
		got, err := findReviewLedgerByShipRun(root, ship)
		if err != nil || got != "review-b" {
			t.Errorf("got (%q, %v), want (review-b, nil)", got, err)
		}
	})

	t.Run("a startedAt that does not parse loses to one that parses", func(t *testing.T) {
		for _, bad := range []string{"not a time", ""} {
			for _, badName := range []string{"review-a", "review-b"} {
				goodName := "review-a"
				if badName == "review-a" {
					goodName = "review-b"
				}
				root := dashRoot(t)
				writeMeta(t, root, badName, ship, bad)
				writeMeta(t, root, goodName, ship, "2026-10-07T09:00:00Z")
				got, err := findReviewLedgerByShipRun(root, ship)
				if err != nil || got != goodName {
					t.Errorf("startedAt %q on %s: got (%q, %v), want (%s, nil)", bad, badName, got, err, goodName)
				}
			}
		}
	})

	t.Run("two startedAt values that do not parse pick the larger folder name", func(t *testing.T) {
		root := dashRoot(t)
		writeMeta(t, root, "review-a", ship, "not a time")
		writeMeta(t, root, "review-b", ship, "")
		writeMeta(t, root, "review-0", ship, "also not a time")
		got, err := findReviewLedgerByShipRun(root, ship)
		if err != nil || got != "review-b" {
			t.Errorf("got (%q, %v), want (review-b, nil)", got, err)
		}
	})

	t.Run("entries that are not folders are skipped", func(t *testing.T) {
		root := dashRoot(t)
		writeMeta(t, root, "review-a", ship, "2026-10-07T09:00:00Z")
		// A newer run folder that is reachable only through a link and a
		// file named like a run folder must not win.
		writeMeta(t, root, "other-run", ship, "2026-10-07T09:45:00Z")
		if err := os.Symlink(filepath.Join(ledgerRoot(root), "other-run"), filepath.Join(ledgerRoot(root), "review-link")); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(ledgerRoot(root), "review-x"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		got, err := findReviewLedgerByShipRun(root, ship)
		if err != nil || got != "review-a" {
			t.Errorf("got (%q, %v), want (review-a, nil)", got, err)
		}
	})

	t.Run("a file named like a run folder alone gives no id", func(t *testing.T) {
		root := dashRoot(t)
		if err := os.MkdirAll(ledgerRoot(root), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(ledgerRoot(root), "review-x"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		got, err := findReviewLedgerByShipRun(root, ship)
		if err != nil || got != "" {
			t.Errorf("got (%q, %v), want (\"\", nil)", got, err)
		}
	})

	t.Run("newest startedAt wins", func(t *testing.T) {
		root := dashRoot(t)
		writeMeta(t, root, "review-a", ship, "2026-10-07T09:30:00Z")
		writeMeta(t, root, "review-b", ship, "2026-10-07T09:00:00Z")
		writeMeta(t, root, "review-c", "ship-other", "2026-10-07T09:50:00Z")
		bad := ledgerRunMetaPath(root, "review-d")
		if err := os.MkdirAll(filepath.Dir(bad), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(bad, []byte("{bad"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(ledgerDir(root, "review-e"), 0o755); err != nil {
			t.Fatal(err)
		}
		writeMeta(t, root, "exec-f", ship, "2026-10-07T09:59:00Z")
		got, err := findReviewLedgerByShipRun(root, ship)
		if err != nil || got != "review-a" {
			t.Errorf("got (%q, %v), want (review-a, nil)", got, err)
		}
	})

	t.Run("equal startedAt picks the larger folder name", func(t *testing.T) {
		root := dashRoot(t)
		writeMeta(t, root, "review-b", ship, "2026-10-07T09:00:00Z")
		writeMeta(t, root, "review-a", ship, "2026-10-07T09:00:00Z")
		got, err := findReviewLedgerByShipRun(root, ship)
		if err != nil || got != "review-b" {
			t.Errorf("got (%q, %v), want (review-b, nil)", got, err)
		}
	})

	t.Run("no match", func(t *testing.T) {
		root := dashRoot(t)
		writeMeta(t, root, "review-a", "ship-other", "2026-10-07T09:00:00Z")
		got, err := findReviewLedgerByShipRun(root, ship)
		if err != nil || got != "" {
			t.Errorf("got (%q, %v), want (\"\", nil)", got, err)
		}
	})

	t.Run("empty ship run id", func(t *testing.T) {
		root := dashRoot(t)
		writeMeta(t, root, "review-a", "", "2026-10-07T09:00:00Z")
		got, err := findReviewLedgerByShipRun(root, "")
		if err != nil || got != "" {
			t.Errorf("got (%q, %v), want (\"\", nil)", got, err)
		}
	})

	t.Run("missing ledger folder", func(t *testing.T) {
		got, err := findReviewLedgerByShipRun(dashRoot(t), ship)
		if err != nil || got != "" {
			t.Errorf("got (%q, %v), want (\"\", nil)", got, err)
		}
	})

	t.Run("ledger path is a file", func(t *testing.T) {
		root := dashRoot(t)
		path := filepath.Join(root, paths.DataDir, paths.RunsSubdir, "ledger")
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		got, err := findReviewLedgerByShipRun(root, ship)
		if err == nil || got != "" {
			t.Errorf("got (%q, %v), want an error and no id", got, err)
		}
	})
}
