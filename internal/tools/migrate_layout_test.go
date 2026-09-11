package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
)

// TestMigrateLayoutMovesTopLevelStateFile covers the basic case: a
// top-level state JSON file directly under execution/ is moved into
// runs/, and the source is gone afterward.
func TestMigrateLayoutMovesTopLevelStateFile(t *testing.T) {
	root := t.TempDir()

	execDir := filepath.Join(root, paths.DataDir, "execution")
	if err := os.MkdirAll(execDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestJSON(t, filepath.Join(execDir, "execute-main-20250101T000000Z.json"), map[string]any{"runStatus": "in-progress"})

	out, err := migrate(root, MigrateIn{Action: "layout"})
	if err != nil {
		t.Fatalf("migrate layout: %v", err)
	}
	if !out.OK {
		t.Fatalf("expected OK, got %+v", out)
	}
	if out.DryRun {
		t.Fatalf("expected DryRun=false, got %+v", out)
	}

	runsFile := filepath.Join(root, paths.DataDir, paths.RunsSubdir, "execute-main-20250101T000000Z.json")
	got := readTestJSON(t, runsFile)
	if got["runStatus"] != "in-progress" {
		t.Fatalf("expected moved file to contain runStatus=in-progress, got %v", got)
	}

	if _, err := os.Stat(filepath.Join(execDir, "execute-main-20250101T000000Z.json")); !os.IsNotExist(err) {
		t.Fatalf("expected source file removed after move, stat err=%v", err)
	}

	wantChanged := []string{paths.DataDir + "/" + paths.RunsSubdir + "/execute-main-20250101T000000Z.json"}
	if len(out.Changed) != 1 || out.Changed[0] != wantChanged[0] {
		t.Fatalf("expected Changed=%v, got %v", wantChanged, out.Changed)
	}
}

// TestMigrateLayoutDryRunWritesNothing verifies dryRun:true reports what
// would move without touching the filesystem at all.
func TestMigrateLayoutDryRunWritesNothing(t *testing.T) {
	root := t.TempDir()

	execDir := filepath.Join(root, paths.DataDir, "execution")
	if err := os.MkdirAll(execDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestJSON(t, filepath.Join(execDir, "plan-main-20250101T000000Z.json"), map[string]any{"a": 1})

	out, err := migrate(root, MigrateIn{Action: "layout", DryRun: true})
	if err != nil {
		t.Fatalf("migrate layout dry-run: %v", err)
	}
	if !out.DryRun {
		t.Fatalf("expected DryRun=true, got %+v", out)
	}

	wantChanged := []string{paths.DataDir + "/" + paths.RunsSubdir + "/plan-main-20250101T000000Z.json"}
	if len(out.Changed) != 1 || out.Changed[0] != wantChanged[0] {
		t.Fatalf("expected Changed=%v, got %v", wantChanged, out.Changed)
	}

	// Source must remain untouched, and nothing must exist at destination.
	if _, err := os.Stat(filepath.Join(execDir, "plan-main-20250101T000000Z.json")); err != nil {
		t.Fatalf("expected source file to remain after dry-run, got err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(root, paths.DataDir, paths.RunsSubdir, "plan-main-20250101T000000Z.json")); !os.IsNotExist(err) {
		t.Fatalf("dry-run must not write destination, stat err=%v", err)
	}
}

// TestMigrateLayoutMissingExecutionDirIsNoop covers idempotency: a project
// that never had (or already fully migrated) the old layout returns OK
// without error.
func TestMigrateLayoutMissingExecutionDirIsNoop(t *testing.T) {
	root := t.TempDir()

	out, err := migrate(root, MigrateIn{Action: "layout"})
	if err != nil {
		t.Fatalf("migrate layout: %v", err)
	}
	if !out.OK {
		t.Fatalf("expected OK with no execution/ directory, got %+v", out)
	}
	if len(out.Changed) != 0 {
		t.Fatalf("expected no changes with no execution/ directory, got %v", out.Changed)
	}
}

// TestMigrateLayoutEmptyExecutionDirIsNoop covers idempotency for the case
// where execution/ exists (e.g. left over after a prior migration) but is
// empty.
func TestMigrateLayoutEmptyExecutionDirIsNoop(t *testing.T) {
	root := t.TempDir()

	execDir := filepath.Join(root, paths.DataDir, "execution")
	if err := os.MkdirAll(execDir, 0o755); err != nil {
		t.Fatal(err)
	}

	out, err := migrate(root, MigrateIn{Action: "layout"})
	if err != nil {
		t.Fatalf("migrate layout: %v", err)
	}
	if !out.OK {
		t.Fatalf("expected OK with empty execution/ directory, got %+v", out)
	}
	if len(out.Changed) != 0 {
		t.Fatalf("expected no changes with empty execution/ directory, got %v", out.Changed)
	}
}

// TestMigrateLayoutConflictIsSkippedNotOverwritten covers the conflict
// rule: when a name already exists under runs/, that entry is left alone
// at the source (not overwritten) and the rest of the migration still
// succeeds.
func TestMigrateLayoutConflictIsSkippedNotOverwritten(t *testing.T) {
	root := t.TempDir()

	execDir := filepath.Join(root, paths.DataDir, "execution")
	if err := os.MkdirAll(execDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestJSON(t, filepath.Join(execDir, "execute-main-20250101T000000Z.json"), map[string]any{"source": "old"})
	// A second, non-conflicting file to prove the rest of the migration
	// still proceeds past the conflict.
	writeTestJSON(t, filepath.Join(execDir, "execute-main-20250102T000000Z.json"), map[string]any{"source": "old2"})

	runsDir := filepath.Join(root, paths.DataDir, paths.RunsSubdir)
	if err := os.MkdirAll(runsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestJSON(t, filepath.Join(runsDir, "execute-main-20250101T000000Z.json"), map[string]any{"source": "new"})

	out, err := migrate(root, MigrateIn{Action: "layout"})
	if err != nil {
		t.Fatalf("migrate layout: %v", err)
	}
	if !out.OK {
		t.Fatalf("expected OK even with a conflict, got %+v", out)
	}

	// The conflicting destination file must be untouched.
	got := readTestJSON(t, filepath.Join(runsDir, "execute-main-20250101T000000Z.json"))
	if got["source"] != "new" {
		t.Fatalf("expected destination conflict file untouched (source=new), got %v", got)
	}
	// The conflicting source file must remain in place (not deleted, not moved).
	oldGot := readTestJSON(t, filepath.Join(execDir, "execute-main-20250101T000000Z.json"))
	if oldGot["source"] != "old" {
		t.Fatalf("expected source conflict file left in place, got %v", oldGot)
	}

	// The non-conflicting file must still have moved.
	movedGot := readTestJSON(t, filepath.Join(runsDir, "execute-main-20250102T000000Z.json"))
	if movedGot["source"] != "old2" {
		t.Fatalf("expected non-conflicting file to have moved, got %v", movedGot)
	}
	if len(out.Changed) != 1 {
		t.Fatalf("expected exactly one Changed entry (the non-conflicting file), got %v", out.Changed)
	}
	wantSkippedLabel := paths.DataDir + "/" + paths.RunsSubdir + "/execute-main-20250101T000000Z.json"
	if !strings.Contains(out.Result, wantSkippedLabel) {
		t.Fatalf("expected Result to report the skipped conflict %q, got %q", wantSkippedLabel, out.Result)
	}
}

// TestMigrateLayoutMergesLedgerIntoExistingRunsLedger covers the trickiest
// case: execution/ledger/<runID>/ subdirectories must be merged into
// runs/ledger/, one runID at a time, even when runs/ledger/ already has
// unrelated entries (as it will for any project where new runs have
// already written ledger data).
func TestMigrateLayoutMergesLedgerIntoExistingRunsLedger(t *testing.T) {
	root := t.TempDir()

	execLedgerDir := filepath.Join(root, paths.DataDir, "execution", "ledger", "run-old")
	if err := os.MkdirAll(execLedgerDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestJSON(t, filepath.Join(execLedgerDir, "worker-1.json"), map[string]any{"status": "checked-out"})

	// runs/ledger/ already exists with an unrelated live run's entry.
	runsLedgerDir := filepath.Join(root, paths.DataDir, paths.RunsSubdir, "ledger", "run-new")
	if err := os.MkdirAll(runsLedgerDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestJSON(t, filepath.Join(runsLedgerDir, "worker-2.json"), map[string]any{"status": "live"})

	out, err := migrate(root, MigrateIn{Action: "layout"})
	if err != nil {
		t.Fatalf("migrate layout: %v", err)
	}
	if !out.OK {
		t.Fatalf("expected OK, got %+v", out)
	}

	// The old run's ledger directory must now live under runs/ledger/.
	movedGot := readTestJSON(t, filepath.Join(root, paths.DataDir, paths.RunsSubdir, "ledger", "run-old", "worker-1.json"))
	if movedGot["status"] != "checked-out" {
		t.Fatalf("expected run-old ledger entry merged into runs/ledger/, got %v", movedGot)
	}

	// The unrelated live run's ledger entry must be untouched.
	liveGot := readTestJSON(t, filepath.Join(runsLedgerDir, "worker-2.json"))
	if liveGot["status"] != "live" {
		t.Fatalf("expected unrelated live ledger entry untouched, got %v", liveGot)
	}

	// The old ledger source directory must be gone (moved, not copied).
	if _, err := os.Stat(execLedgerDir); !os.IsNotExist(err) {
		t.Fatalf("expected execution/ledger/run-old removed after move, stat err=%v", err)
	}

	wantChanged := paths.DataDir + "/" + paths.RunsSubdir + "/ledger/run-old/"
	found := false
	for _, c := range out.Changed {
		if c == wantChanged {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected Changed to include %q, got %v", wantChanged, out.Changed)
	}
}

// TestMigrateLayoutMovesPerRunDirectory covers a per-runID working
// directory (e.g. holding fact sheets) sitting directly under execution/,
// distinct from ledger/ and from top-level state files.
func TestMigrateLayoutMovesPerRunDirectory(t *testing.T) {
	root := t.TempDir()

	runDir := filepath.Join(root, paths.DataDir, "execution", "20250101T000000Z")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "task-1.md"), []byte("fact sheet content"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := migrate(root, MigrateIn{Action: "layout"})
	if err != nil {
		t.Fatalf("migrate layout: %v", err)
	}
	if !out.OK {
		t.Fatalf("expected OK, got %+v", out)
	}

	movedFile := filepath.Join(root, paths.DataDir, paths.RunsSubdir, "20250101T000000Z", "task-1.md")
	data, err := os.ReadFile(movedFile)
	if err != nil || string(data) != "fact sheet content" {
		t.Fatalf("expected fact sheet moved to runs/20250101T000000Z/task-1.md, got err=%v data=%q", err, data)
	}

	if _, err := os.Stat(runDir); !os.IsNotExist(err) {
		t.Fatalf("expected source run directory removed after move, stat err=%v", err)
	}
}
