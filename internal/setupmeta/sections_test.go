package setupmeta

import (
	"reflect"
	"testing"
)

// TestCanonicalSteps_Contents pins CanonicalSteps' exact contents and order.
// This slice seeds the ship.steps setup-wizard field's Options and Default
// (ShipFields, below), so a regression here silently changes what step
// values new projects are offered/defaulted to during /setup. In
// particular, "version" must stay absent — it was deliberately removed
// (folded into other steps) and must not resurface as a selectable step.
func TestCanonicalSteps_Contents(t *testing.T) {
	want := []string{
		"execute", "commit", "review", "verify-openspec",
		"archive-openspec", "pr", "verify-pipeline", "await-remote-review",
		"learnings-commit",
	}
	if !reflect.DeepEqual(CanonicalSteps, want) {
		t.Errorf("CanonicalSteps:\n  got:  %v\n  want: %v", CanonicalSteps, want)
	}
}

// TestCanonicalSteps_VersionAbsent is a focused regression guard: "version"
// must never appear in CanonicalSteps.
func TestCanonicalSteps_VersionAbsent(t *testing.T) {
	for _, step := range CanonicalSteps {
		if step == "version" {
			t.Fatalf(`CanonicalSteps must not contain "version", got %v`, CanonicalSteps)
		}
	}
}

// TestShipFields_StepsOptionsMatchCanonicalSteps confirms the "steps" field
// in ShipFields uses CanonicalSteps for both Options and Default, per the
// package's documented identity invariant with scripts/lib/ship-fields.js.
func TestShipFields_StepsOptionsMatchCanonicalSteps(t *testing.T) {
	var steps *Field
	for i := range ShipFields {
		if ShipFields[i].Name == "steps" {
			steps = &ShipFields[i]
			break
		}
	}
	if steps == nil {
		t.Fatal(`ShipFields has no "steps" field`)
	}
	if !reflect.DeepEqual(steps.Options, CanonicalSteps) {
		t.Errorf("ShipFields[steps].Options:\n  got:  %v\n  want: %v", steps.Options, CanonicalSteps)
	}
	if !reflect.DeepEqual(steps.Default, CanonicalSteps) {
		t.Errorf("ShipFields[steps].Default:\n  got:  %v\n  want: %v", steps.Default, CanonicalSteps)
	}
}

// TestSections_Count pins the total number of setup sections (17, after the
// "automation" section was added).
func TestSections_Count(t *testing.T) {
	if got := len(Sections()); got != 17 {
		t.Errorf("Sections() returned %d sections, want 17", got)
	}
}

// TestAutomationSection verifies the "automation" section descriptor: its
// storage location, consuming skills, and the presence/shape of all 7
// automationFields entries (mode, report.enabled, report.format,
// drift.maxErrorRate, drift.maxWarningRate, drift.minErrorFloor,
// push.featureBranchAutoApprove).
func TestAutomationSection(t *testing.T) {
	var automation *Section
	sections := Sections()
	for i := range sections {
		if sections[i].ID == "automation" {
			automation = &sections[i]
			break
		}
	}
	if automation == nil {
		t.Fatal(`Sections() has no "automation" section`)
	}

	if automation.ConfigFile != ".sdlc-v2/local.json" {
		t.Errorf("automation.ConfigFile = %q, want %q", automation.ConfigFile, ".sdlc-v2/local.json")
	}
	if automation.ConfigPath != "automation" {
		t.Errorf("automation.ConfigPath = %q, want %q", automation.ConfigPath, "automation")
	}
	wantConsumedBy := []string{"execute", "ship"}
	if !reflect.DeepEqual(automation.ConsumedBy, wantConsumedBy) {
		t.Errorf("automation.ConsumedBy = %v, want %v", automation.ConsumedBy, wantConsumedBy)
	}
	if !automation.Optional {
		t.Error("automation.Optional = false, want true")
	}

	wantFieldNames := []string{
		"mode",
		"report.enabled",
		"report.format",
		"drift.maxErrorRate",
		"drift.maxWarningRate",
		"drift.minErrorFloor",
		"push.featureBranchAutoApprove",
	}
	if len(automation.Fields) != len(wantFieldNames) {
		t.Fatalf("automation.Fields has %d entries, want %d", len(automation.Fields), len(wantFieldNames))
	}
	for i, name := range wantFieldNames {
		if automation.Fields[i].Name != name {
			t.Errorf("automation.Fields[%d].Name = %q, want %q", i, automation.Fields[i].Name, name)
		}
	}

	var mode *Field
	for i := range automation.Fields {
		if automation.Fields[i].Name == "mode" {
			mode = &automation.Fields[i]
		}
	}
	if mode == nil {
		t.Fatal(`automation.Fields has no "mode" entry`)
	}
	wantModeOptions := []string{"supervised", "unattended"}
	if !reflect.DeepEqual(mode.Options, wantModeOptions) {
		t.Errorf("automation mode.Options = %v, want %v", mode.Options, wantModeOptions)
	}
	if mode.Default != "supervised" {
		t.Errorf("automation mode.Default = %v, want %q", mode.Default, "supervised")
	}
}
