// Package tools: tests for setup_write_sections's scaffold_ci auto-trigger
// (Task 10 addition).
package tools

import (
	"path/filepath"
	"testing"
)

// TestSetupWriteSections_VersionTriggersScaffold verifies that writing a
// "version" section auto-triggers scaffold_ci and reports the resulting
// file actions on the output.
func TestSetupWriteSections_VersionTriggersScaffold(t *testing.T) {
	root := t.TempDir()

	out, err := setupWriteSections(root, SetupWriteSectionsIn{
		SectionsJSON: `{"version":{"mode":"tag","tagPrefix":"v"}}`,
	})
	if err != nil {
		t.Fatalf("setupWriteSections: %v", err)
	}
	if !out.OK {
		t.Fatalf("expected OK=true, got errors: %v", out.Errors)
	}
	if len(out.Written) != 1 || out.Written[0] != "version" {
		t.Fatalf("expected written=[version], got %v", out.Written)
	}

	if len(out.Scaffold) != 10 {
		t.Fatalf("expected 10 scaffold file reports, got %d", len(out.Scaffold))
	}
	for _, f := range out.Scaffold {
		if f.Action != "created" {
			t.Errorf("file %s: expected action 'created', got %q", f.Path, f.Action)
		}
	}

	destPath := filepath.Join(root, ".github", "workflows", "release-on-main.yml")
	if !scaffoldFileExists(destPath) {
		t.Errorf("release-on-main.yml not written to disk at %s", destPath)
	}
}

// TestSetupWriteSections_NonVersionSkipsScaffold verifies that writing a
// section batch without "version" does not trigger scaffold_ci.
func TestSetupWriteSections_NonVersionSkipsScaffold(t *testing.T) {
	root := t.TempDir()

	out, err := setupWriteSections(root, SetupWriteSectionsIn{
		SectionsJSON: `{"commit":{"style":"conventional"}}`,
	})
	if err != nil {
		t.Fatalf("setupWriteSections: %v", err)
	}
	if !out.OK {
		t.Fatalf("expected OK=true, got errors: %v", out.Errors)
	}
	if out.Scaffold != nil {
		t.Errorf("expected no scaffold reports, got %v", out.Scaffold)
	}

	destPath := filepath.Join(root, ".github", "workflows", "release-on-main.yml")
	if scaffoldFileExists(destPath) {
		t.Errorf("release-on-main.yml unexpectedly written to disk at %s", destPath)
	}
}

// TestSetupWriteSections_VersionScaffoldIdempotent verifies a second call
// with "version" re-triggers scaffold_ci non-destructively (all skipped,
// no error, config write still succeeds).
func TestSetupWriteSections_VersionScaffoldIdempotent(t *testing.T) {
	root := t.TempDir()

	if _, err := setupWriteSections(root, SetupWriteSectionsIn{
		SectionsJSON: `{"version":{"mode":"tag","tagPrefix":"v"}}`,
	}); err != nil {
		t.Fatalf("setupWriteSections (first): %v", err)
	}

	out, err := setupWriteSections(root, SetupWriteSectionsIn{
		SectionsJSON: `{"version":{"mode":"tag","tagPrefix":"v"}}`,
	})
	if err != nil {
		t.Fatalf("setupWriteSections (second): %v", err)
	}
	if !out.OK {
		t.Fatalf("expected OK=true, got errors: %v", out.Errors)
	}
	for _, f := range out.Scaffold {
		if f.Action != "skipped" {
			t.Errorf("file %s: expected action 'skipped' on second run, got %q", f.Path, f.Action)
		}
	}
}
