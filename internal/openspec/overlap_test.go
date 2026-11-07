package openspec

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// mkSpec creates <root>/<rel>/specs/<cap>/spec.md for each capability.
func mkSpec(t *testing.T, root, rel string, caps ...string) {
	t.Helper()
	if len(caps) == 0 {
		if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(rel)), 0o755); err != nil {
			t.Fatal(err)
		}
		return
	}
	for _, c := range caps {
		dir := filepath.Join(root, filepath.FromSlash(rel), "specs", c)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "spec.md"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCapabilityOverlaps_ActiveAndStaged(t *testing.T) {
	active := t.TempDir()
	staging := t.TempDir()
	stagingRel := ".sdlc-v2/openspec-staging/"
	mkSpec(t, active, "openspec/changes/mine", "ship", "plan")
	mkSpec(t, active, "openspec/changes/zeta", "ship")
	mkSpec(t, active, "openspec/changes/alpha", "ship", "other")
	mkSpec(t, staging, stagingRel+"beta", "plan", "unrelated")

	got, skipped := CapabilityOverlaps(active, staging, "mine")
	want := []CapabilityOverlap{
		{Capability: "plan", OtherChange: "beta", Source: "staged"},
		{Capability: "ship", OtherChange: "alpha", Source: "active"},
		{Capability: "ship", OtherChange: "zeta", Source: "active"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("overlaps = %+v, want %+v", got, want)
	}
	if len(skipped) != 0 {
		t.Fatalf("skipped = %v, want none", skipped)
	}
}

func TestCapabilityOverlaps_NoSharedCapability(t *testing.T) {
	active := t.TempDir()
	mkSpec(t, active, "openspec/changes/mine", "ship")
	mkSpec(t, active, "openspec/changes/other", "plan")

	got, skipped := CapabilityOverlaps(active, active, "mine")
	if len(got) != 0 || len(skipped) != 0 {
		t.Fatalf("got %+v skipped %v, want none", got, skipped)
	}
}

func TestCapabilityOverlaps_SkipsInvalidAndArchiveAndSelf(t *testing.T) {
	active := t.TempDir()
	staging := t.TempDir()
	mkSpec(t, active, "openspec/changes/mine", "ship")
	mkSpec(t, active, "openspec/changes/archive/2026-01-01-old", "ship")
	mkSpec(t, active, "openspec/changes/archive", "ship")
	mkSpec(t, staging, ".sdlc-v2/openspec-staging/.tmp-x-1", "ship")
	mkSpec(t, staging, ".sdlc-v2/openspec-staging/.old-x-1", "ship")
	if err := os.WriteFile(filepath.Join(staging, ".sdlc-v2/openspec-staging/x.lock"), []byte("1"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The staged copy of the change itself is never listed.
	mkSpec(t, staging, ".sdlc-v2/openspec-staging/mine", "ship")

	got, skipped := CapabilityOverlaps(active, staging, "mine")
	if len(got) != 0 || len(skipped) != 0 {
		t.Fatalf("got %+v skipped %v, want none", got, skipped)
	}
}

func TestCapabilityOverlaps_ActiveWinsOverStagedCopy(t *testing.T) {
	active := t.TempDir()
	staging := t.TempDir()
	mkSpec(t, active, "openspec/changes/mine", "ship")
	mkSpec(t, active, "openspec/changes/both", "ship")
	mkSpec(t, staging, ".sdlc-v2/openspec-staging/both", "ship")

	got, _ := CapabilityOverlaps(active, staging, "mine")
	want := []CapabilityOverlap{{Capability: "ship", OtherChange: "both", Source: "active"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("overlaps = %+v, want %+v", got, want)
	}
}

func TestCapabilityOverlaps_OwnSpecsFromStaging(t *testing.T) {
	active := t.TempDir()
	staging := t.TempDir()
	mkSpec(t, staging, ".sdlc-v2/openspec-staging/mine", "ship")
	mkSpec(t, active, "openspec/changes/other", "ship")

	got, _ := CapabilityOverlaps(active, staging, "mine")
	if len(got) != 1 || got[0].OtherChange != "other" {
		t.Fatalf("overlaps = %+v, want one pair with other", got)
	}
}

func TestCapabilityOverlaps_ReadErrorSkipsOneChange(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	active := t.TempDir()
	mkSpec(t, active, "openspec/changes/mine", "ship")
	mkSpec(t, active, "openspec/changes/broken", "ship")
	mkSpec(t, active, "openspec/changes/good", "ship")
	brokenSpecs := filepath.Join(active, "openspec/changes/broken/specs")
	if err := os.Chmod(brokenSpecs, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(brokenSpecs, 0o755) })

	got, skipped := CapabilityOverlaps(active, active, "mine")
	if len(got) != 1 || got[0].OtherChange != "good" {
		t.Fatalf("overlaps = %+v, want one pair with good", got)
	}
	if len(skipped) != 1 || !strings.HasPrefix(skipped[0], `"broken": `) {
		t.Fatalf("skipped = %v, want one entry for broken", skipped)
	}
}

func TestCapabilityOverlaps_NoOwnSpecs(t *testing.T) {
	active := t.TempDir()
	mkSpec(t, active, "openspec/changes/mine")
	mkSpec(t, active, "openspec/changes/other", "ship")

	got, skipped := CapabilityOverlaps(active, active, "mine")
	if len(got) != 0 || len(skipped) != 0 {
		t.Fatalf("got %+v skipped %v, want none", got, skipped)
	}
}
