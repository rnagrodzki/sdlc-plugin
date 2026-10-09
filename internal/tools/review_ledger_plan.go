package tools

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
)

// reviewPlanRow is one dimension of a review run: a planned dimension of
// run.meta, or a worker file when run.meta plans no dimensions.
type reviewPlanRow struct {
	Wave        int    // 1-based wave of the plan; 0 when run.meta plans no dimensions
	Name        string // dimension name; the worker id when run.meta plans no dimensions
	WorkerID    string
	Status      string // StepPending | StepInProgress | StepCompleted | StepSkipped
	Reason      string // a reviewStop* constant when Status is StepSkipped, else ""
	Findings    int
	Worst       string // highest dashboardSeverity value of the findings, "" with none
	DurationSec int    // checkoutAt minus checkinAt; 0 when not checked out

	hasFile    bool               // a readable worker file exists
	checkinAt  time.Time          // zero when absent or not parseable
	checkoutAt time.Time          // zero when absent or not parseable
	findings   []dashboardFinding // the findings of the worker file, in file order
}

// reviewPlanTotals counts how much of the review run plan ran. Every count is
// 0 when run.meta holds no planned dimensions.
type reviewPlanTotals struct {
	WavesPlanned, WavesRun, DimensionsPlanned, DimensionsRun, NeverStarted int
}

// readReviewRunMeta reads and parses the run.meta file of the ledger run
// folder dir. ok is false when the file is missing, cannot be read, or does
// not parse.
func readReviewRunMeta(dir string) (meta reviewRunMeta, ok bool) {
	b, err := os.ReadFile(filepath.Join(dir, ledgerRunMetaFile))
	if err != nil {
		return reviewRunMeta{}, false
	}
	if err := json.Unmarshal(b, &meta); err != nil {
		return reviewRunMeta{}, false
	}
	return meta, true
}

// readReviewLedgerPlan reads the ledger run folder of runID: run.meta and
// every worker file. With planned dimensions in run.meta, the rows are those
// dimensions in run.meta order. A worker file that no dimension plans is not
// a row, so the row count equals DimensionsPlanned. With no run.meta, a
// run.meta that does not parse, or a run.meta with no dimensions, the rows are
// the worker files in file name order, each with wave 0, and the totals are 0. updated is the newest modification time
// of run.meta and the worker files. A missing folder gives no rows and no
// error; err is set only when the folder exists and cannot be read.
func readReviewLedgerPlan(root, runID string) (rows []reviewPlanRow, totals reviewPlanTotals, updated time.Time, err error) {
	return readReviewLedgerDir(ledgerDir(root, runID))
}

// readReviewLedgerDir is readReviewLedgerPlan for the ledger run folder dir.
func readReviewLedgerDir(dir string) (rows []reviewPlanRow, totals reviewPlanTotals, updated time.Time, err error) {
	files, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, reviewPlanTotals{}, time.Time{}, nil
		}
		return nil, reviewPlanTotals{}, time.Time{}, fmt.Errorf("read review ledger %s: %w", dir, err)
	}

	workers := map[string]reviewPlanRow{}
	var order []string // worker ids in file name order
	for _, f := range files {
		if !strings.HasSuffix(f.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, f.Name())
		row, ok := reviewWorkerRow(path, strings.TrimSuffix(f.Name(), ".json"))
		if !ok {
			continue
		}
		updated = dashboardLatest(updated, dashboardModTime(path))
		workers[row.WorkerID] = row
		order = append(order, row.WorkerID)
	}
	updated = dashboardLatest(updated, dashboardModTime(filepath.Join(dir, ledgerRunMetaFile)))

	meta, ok := readReviewRunMeta(dir)
	if !ok || len(meta.Dimensions) == 0 {
		for _, id := range order {
			rows = append(rows, workers[id])
		}
		return rows, reviewPlanTotals{}, updated, nil
	}

	wavesRun := map[int]bool{}
	totals.WavesPlanned = len(meta.Waves)
	totals.DimensionsPlanned = len(meta.Dimensions)
	for _, d := range meta.Dimensions {
		row, has := workers[d.WorkerID]
		if !has {
			row = reviewPlanRow{WorkerID: d.WorkerID}
		}
		row.Name = d.Name
		row.Wave = d.Wave
		row.Status, row.Reason = reviewPlanStatus(row, d.StopReason)
		if row.hasFile {
			totals.DimensionsRun++
			wavesRun[d.Wave] = true
		} else {
			totals.NeverStarted++
		}
		rows = append(rows, row)
	}
	totals.WavesRun = len(wavesRun)
	return rows, totals, updated, nil
}

// reviewWorkerRow reads the worker file at path into a row named workerID
// with wave 0. Status is StepCompleted when the file has checkoutAt, else
// StepInProgress. ok is false when the file cannot be read or does not parse.
func reviewWorkerRow(path, workerID string) (reviewPlanRow, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return reviewPlanRow{}, false
	}
	var dim dashboardReviewDim
	if err := json.Unmarshal(b, &dim); err != nil {
		return reviewPlanRow{}, false
	}
	row := reviewPlanRow{Name: workerID, WorkerID: workerID, hasFile: true}
	row.checkinAt, _ = dashboardParseTime(dim.CheckinAt)
	row.checkoutAt, _ = dashboardParseTime(dim.CheckoutAt)
	row.Status = StepInProgress
	if dim.CheckoutAt != "" {
		row.Status = StepCompleted
	}
	if !row.checkinAt.IsZero() && !row.checkoutAt.IsZero() && row.checkoutAt.After(row.checkinAt) {
		row.DurationSec = int(row.checkoutAt.Sub(row.checkinAt) / time.Second)
	}
	row.findings = dashboardParseFindings(dim.Findings)
	row.Findings = len(row.findings)
	for _, f := range row.findings {
		if sev := dashboardSeverity(f.Severity); severityRank[sev] > severityRank[row.Worst] {
			row.Worst = sev
		}
	}
	return row, true
}

// reviewPlanStatus returns the status and reason of a planned dimension from
// its worker row and its run.meta stopReason. A checked-out worker is
// completed whatever the stopReason. Else a stopReason gives skipped with that
// reason. Else a worker file gives in_progress, and no worker file gives
// pending.
func reviewPlanStatus(row reviewPlanRow, stopReason string) (status, reason string) {
	switch {
	case row.hasFile && row.Status == StepCompleted:
		return StepCompleted, ""
	case stopReason != "":
		return StepSkipped, stopReason
	case row.hasFile:
		return StepInProgress, ""
	}
	return StepPending, ""
}

// findReviewLedgerByShipRun returns the id of the review ledger run folder
// whose run.meta shipRunId is shipRunID. Only real folders whose name starts
// with "review-" count; a file or a link of that name is skipped. Of several
// such folders, the newest run.meta startedAt wins. A startedAt that is empty
// or does not parse counts as the zero time, so it loses to any startedAt that
// parses. When two startedAt values are equal, or both are unparseable, the
// larger folder name wins. A folder whose run.meta is missing or does not
// parse is skipped. It returns "" and no error when shipRunID is empty, when
// the ledger folder is missing, or when no folder matches. err is set only
// when the ledger folder exists and cannot be read.
func findReviewLedgerByShipRun(root, shipRunID string) (runID string, err error) {
	if shipRunID == "" {
		return "", nil
	}
	dir := filepath.Join(root, paths.DataDir, paths.RunsSubdir, "ledger")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", fmt.Errorf("read review ledger folder %s: %w", dir, err)
	}
	var bestAt time.Time
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), dashboardReviewPrefix) {
			continue
		}
		meta, ok := readReviewRunMeta(filepath.Join(dir, e.Name()))
		if !ok || meta.ShipRunID != shipRunID {
			continue
		}
		at, _ := dashboardParseTime(meta.StartedAt)
		if runID == "" || at.After(bestAt) || (at.Equal(bestAt) && e.Name() > runID) {
			runID, bestAt = e.Name(), at
		}
	}
	return runID, nil
}
