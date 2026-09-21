package tools

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
)

func shipErrSkipAsRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permission bits")
	}
}

// shipErrChmod restores 0o755 in t.Cleanup so t.TempDir can remove the tree.
func shipErrChmod(t *testing.T, dir string, mode os.FileMode) {
	t.Helper()
	shipErrSkipAsRoot(t)
	if err := os.Chmod(dir, mode); err != nil {
		t.Fatalf("chmod %s: %v", dir, err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
}

func requireShipPermissionCause(t *testing.T, cause error) {
	t.Helper()
	if !errors.Is(cause, fs.ErrPermission) {
		t.Errorf("cause = %v, want a permission error", cause)
	}
}

// ---------------------------------------------------------------------------
// 0o500 state dir: lookups work, every write fails
// ---------------------------------------------------------------------------

func TestShipState_ReadOnlyStateDir_WriteFailures(t *testing.T) {
	dir := t.TempDir()
	path := shipStateInitFixture(t, dir, shipErrBranch)

	// cleanup and cleanup-pipeline only reach their write once every step is terminal.
	steps, _ := readStateData(t, path)["steps"].([]any)
	for _, s := range steps {
		step, _ := s.(map[string]any)
		name, _ := step["name"].(string)
		setStepStatus(t, path, name, "completed", nil)
	}

	shipErrChmod(t, filepath.Dir(path), 0o500)
	before := shipErrReadFile(t, path)

	// Lookups still succeed here, so each failure below comes from the write.
	if _, err := shipState(dir, dir, ShipStateIn{Action: "read", Detail: shipErrWithBranch(nil)}, shipErrNow); err != nil {
		t.Fatalf("read in the read-only dir: %v", err)
	}

	tests := []ShipStateIn{
		{Action: "start", Step: "execute"},
		{Action: "complete", Step: "execute"},
		{Action: "begin-step", Step: "execute"},
		{Action: "complete-step", Step: "execute"},
		{Action: "skip", Step: "execute"},
		{Action: "fail", Step: "execute", Detail: map[string]any{"error": "boom"}},
		{Action: "decide", Step: "execute", Detail: map[string]any{"text": "go"}},
		{Action: "defer", Detail: map[string]any{"severity": "low", "file": "a.go", "title": "t"}},
		{Action: "cleanup"},
		{Action: "cleanup-pipeline"},
	}
	for _, in := range tests {
		t.Run(in.Action, func(t *testing.T) {
			in.Detail = shipErrWithBranch(in.Detail)
			_, err := shipState(dir, dir, in, shipErrNow)
			_, cause := requireShipErr(t, err, shipErrInfra)
			requireShipPermissionCause(t, cause)
			if !bytes.Equal(before, shipErrReadFile(t, path)) {
				t.Error("state file changed although the write failed")
			}
		})
	}
}

func TestShipState_Init_ReadOnlyStateDir(t *testing.T) {
	t.Run("state file cannot be created", func(t *testing.T) {
		root := t.TempDir()
		runs := filepath.Join(root, paths.DataDir, paths.RunsSubdir)
		if err := os.MkdirAll(runs, 0o755); err != nil {
			t.Fatal(err)
		}
		shipErrChmod(t, runs, 0o500)

		_, err := shipState(root, root, ShipStateIn{Action: "init", Detail: shipErrWithBranch(nil)}, shipErrNow)
		_, cause := requireShipErr(t, err, shipErrInfra)
		requireShipPermissionCause(t, cause)
		if left, _ := filepath.Glob(filepath.Join(runs, "ship-*")); len(left) != 0 {
			t.Errorf("files left behind by a failed init: %v", left)
		}
	})

	t.Run("state file cannot be rewritten after it was created", func(t *testing.T) {
		shipErrSkipAsRoot(t)
		root := t.TempDir()
		runs := filepath.Join(root, paths.DataDir, paths.RunsSubdir)
		if err := os.MkdirAll(runs, 0o755); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(runs, 0o755) })

		// init calls now() after it creates the bare file and before it rewrites it.
		lockAfterCreate := func() time.Time {
			if err := os.Chmod(runs, 0o500); err != nil {
				t.Errorf("chmod %s: %v", runs, err)
			}
			return shipErrNow()
		}
		_, err := shipState(root, root, ShipStateIn{Action: "init", Detail: shipErrWithBranch(nil)}, lockAfterCreate)
		_, cause := requireShipErr(t, err, shipErrInfra)
		requireShipPermissionCause(t, cause)
		if left, _ := filepath.Glob(filepath.Join(runs, "ship-*.json")); len(left) != 1 {
			t.Errorf("state files = %v, want only the bare file created before the failed rewrite", left)
		}
	})
}

func TestShipState_Migrate_RenameFailure(t *testing.T) {
	root := t.TempDir()
	runs := filepath.Join(root, paths.DataDir, paths.RunsSubdir)
	oldFile := filepath.Join(runs, "ship-feat-old-20200101T000000Z.json")
	writeFile(t, oldFile, `{}`)
	shipErrChmod(t, runs, 0o500)

	_, err := shipState(root, root, ShipStateIn{
		Action: "migrate",
		Detail: map[string]any{"from": "feat/old", "to": "feat/new"},
	}, shipErrNow)
	_, cause := requireShipErr(t, err, shipErrInfra)
	requireShipPermissionCause(t, cause)
	if _, statErr := os.Stat(oldFile); statErr != nil {
		t.Errorf("source file must stay in place after a failed rename: %v", statErr)
	}
}

// ---------------------------------------------------------------------------
// File removed between the directory listing and the stat of its entry
// ---------------------------------------------------------------------------

func TestShipState_GC_DryRun_SkipsEntryRemovedDuringScan(t *testing.T) {
	root := t.TempDir()
	runs := filepath.Join(root, paths.DataDir, paths.RunsSubdir)
	vanishing := filepath.Join(runs, "ship-feat-vanishing-20200101T000000Z.json")
	staying := filepath.Join(runs, "ship-feat-staying-20200101T000000Z.json")
	writeFile(t, vanishing, `{}`)
	writeFile(t, staying, `{}`)

	// The dry run calls now() between listing the directory and stat-ing its entries.
	removeAfterListing := func() time.Time {
		if err := os.Remove(vanishing); err != nil {
			t.Errorf("remove %s: %v", vanishing, err)
		}
		return shipErrNow()
	}
	out, err := shipState(root, root, ShipStateIn{
		Action: "gc",
		Detail: map[string]any{"dryRun": true},
	}, removeAfterListing)
	if err != nil {
		t.Fatalf("gc dry-run: %v", err)
	}
	m, _ := out.(map[string]any)
	bucket, _ := m["ship"].(map[string]any)

	if got, _ := bucket["wouldDelete"].([]any); len(got) != 0 {
		t.Errorf("ship.wouldDelete = %v, want empty", got)
	}
	keep, _ := bucket["wouldKeep"].([]any)
	if len(keep) != 1 {
		t.Fatalf("ship.wouldKeep = %v, want only the file that still exists", keep)
	}
	if entry, _ := keep[0].(map[string]any); entry["file"] != filepath.Base(staying) {
		t.Errorf("ship.wouldKeep[0] = %v, want file %s", entry, filepath.Base(staying))
	}
}

// ---------------------------------------------------------------------------
// State dir locked after the lookup listed it and before the GC sweep does
// ---------------------------------------------------------------------------

func TestShipState_CleanupPipeline_GCSweepFailure(t *testing.T) {
	shipErrSkipAsRoot(t)

	tests := []struct {
		name   string
		detail map[string]any
	}{
		{"force", map[string]any{"force": true}},
		{"no state file", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			runs := filepath.Join(root, paths.DataDir, paths.RunsSubdir)
			if err := os.MkdirAll(runs, 0o755); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chmod(runs, 0o755) })

			// The sweep runs `git branch --list` after the lookup and before it lists the dir.
			stubDir := t.TempDir()
			script := "#!/bin/sh\n[ \"$1\" = branch ] && chmod 0 \"$SHIP_ERR_LOCK_DIR\"\nexit 0\n"
			if err := os.WriteFile(filepath.Join(stubDir, "git"), []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", stubDir+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("SHIP_ERR_LOCK_DIR", runs)

			in := ShipStateIn{Action: "cleanup-pipeline", Detail: shipErrWithBranch(tt.detail)}
			_, err := shipState(root, root, in, shipErrNow)
			_, cause := requireShipErr(t, err, shipErrInfra)
			requireShipPermissionCause(t, cause)
		})
	}
}

// ---------------------------------------------------------------------------
// 0o000 state dir: even listing it fails
// ---------------------------------------------------------------------------

func TestShipState_UnreadableStateDir(t *testing.T) {
	dir := t.TempDir()
	path := shipStateInitFixture(t, dir, shipErrBranch)
	shipErrChmod(t, filepath.Dir(path), 0o000)

	tests := []struct {
		name string
		in   ShipStateIn
	}{
		{"read", ShipStateIn{Action: "read"}},
		{"init", ShipStateIn{Action: "init"}},
		{"cleanup", ShipStateIn{Action: "cleanup"}},
		{"cleanup-pipeline", ShipStateIn{Action: "cleanup-pipeline"}},
		{"gc", ShipStateIn{Action: "gc"}},
		{"gc dry run", ShipStateIn{Action: "gc", Detail: map[string]any{"dryRun": true}}},
		{"migrate", ShipStateIn{Action: "migrate", Detail: map[string]any{"from": "feat/a", "to": "feat/b"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.in.Detail = shipErrWithBranch(tt.in.Detail)
			_, err := shipState(dir, dir, tt.in, shipErrNow)
			_, cause := requireShipErr(t, err, shipErrInfra)
			requireShipPermissionCause(t, cause)
		})
	}
}
