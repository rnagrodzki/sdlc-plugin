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
