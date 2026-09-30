package shipmeta

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestInitialShipStepsFromConfig_OrderAndKind verifies that
// InitialShipStepsFromConfig seeds exactly one entry per input step, in the
// given order, each starting "pending" and classified "tracked" (a
// TrackedShipSteps member) or "inline" (everything else).
func TestInitialShipStepsFromConfig_OrderAndKind(t *testing.T) {
	in := []string{"execute", "verify-openspec", "commit", "archive-openspec", "review", "pr", "learnings-commit"}
	want := []ShipStateStep{
		{Name: "execute", Status: "pending", Kind: "tracked"},
		{Name: "verify-openspec", Status: "pending", Kind: "inline"},
		{Name: "commit", Status: "pending", Kind: "tracked"},
		{Name: "archive-openspec", Status: "pending", Kind: "inline"},
		{Name: "review", Status: "pending", Kind: "tracked"},
		{Name: "pr", Status: "pending", Kind: "tracked"},
		{Name: "learnings-commit", Status: "pending", Kind: "inline"},
	}

	got := InitialShipStepsFromConfig(in)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("InitialShipStepsFromConfig(%v) = %#v, want %#v", in, got, want)
	}
}

// TestInitialShipStepsFromConfig_HardenIsInline verifies the opt-in "harden"
// step is seeded as an inline step (no dedicated begin/complete lifecycle).
func TestInitialShipStepsFromConfig_HardenIsInline(t *testing.T) {
	got := InitialShipStepsFromConfig([]string{"execute", "commit", "review", "harden", "pr"})
	want := ShipStateStep{Name: "harden", Status: "pending", Kind: "inline"}
	if len(got) != 5 || !reflect.DeepEqual(got[3], want) {
		t.Errorf("InitialShipStepsFromConfig(...harden...) = %#v, want got[3] = %#v", got, want)
	}
}

// TestInitialShipStepsFromConfig_AllCanonicalSteps exercises the full
// 10-name CanonicalSteps list (the F-ship-3 "N-step pipeline shows N rows"
// scenario): 10 entries in, 10 out, in the same order, split 4 tracked / 6
// inline (received-review/commit-fixes are conditional-only and never
// appear in CanonicalSteps, so the tracked set present here is exactly
// execute/commit/review/pr).
func TestInitialShipStepsFromConfig_AllCanonicalSteps(t *testing.T) {
	got := InitialShipStepsFromConfig(CanonicalSteps)
	if len(got) != len(CanonicalSteps) {
		t.Fatalf("len(got) = %d, want %d", len(got), len(CanonicalSteps))
	}

	tracked, inline := 0, 0
	for i, step := range got {
		if step.Name != CanonicalSteps[i] {
			t.Errorf("got[%d].Name = %q, want %q (order not preserved)", i, step.Name, CanonicalSteps[i])
		}
		if step.Status != "pending" {
			t.Errorf("got[%d].Status = %q, want %q", i, step.Status, "pending")
		}
		switch step.Kind {
		case "tracked":
			tracked++
		case "inline":
			inline++
		default:
			t.Errorf("got[%d].Kind = %q, want %q or %q", i, step.Kind, "tracked", "inline")
		}
	}
	if tracked != 4 || inline != 6 {
		t.Errorf("tracked/inline split = %d/%d, want 4/6", tracked, inline)
	}
}

// TestInitialShipStepsFromConfig_UnknownNameIsInline verifies a
// config-sourced name outside ValidSteps (reaches here only as a
// ship_prepare warning, never an error — see ship.go's step-name
// validation) still gets seeded, classified "inline" rather than rejected
// or dropped.
func TestInitialShipStepsFromConfig_UnknownNameIsInline(t *testing.T) {
	got := InitialShipStepsFromConfig([]string{"totally-unknown-step"})
	want := []ShipStateStep{{Name: "totally-unknown-step", Status: "pending", Kind: "inline"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("InitialShipStepsFromConfig = %#v, want %#v", got, want)
	}
}

// TestInitialShipStepsFromConfig_Empty verifies an empty configured step
// list seeds an empty (non-nil) scaffold rather than panicking or returning
// nil (state.Data["steps"] should marshal as `[]`, not `null`).
func TestInitialShipStepsFromConfig_Empty(t *testing.T) {
	got := InitialShipStepsFromConfig([]string{})
	if got == nil {
		t.Fatal("InitialShipStepsFromConfig([]string{}) = nil, want non-nil empty slice")
	}
	if len(got) != 0 {
		t.Errorf("len(got) = %d, want 0", len(got))
	}
}

// TestInitialShipSteps_KindOmitted locks InitialShipSteps' pre-existing
// fixed 6-entry scaffold to remain byte-shape identical to its callers
// (internal/tools/ship_state.go's "init" action): Kind stays the zero value
// (empty string, omitted by its `omitempty` JSON tag) rather than being
// backfilled to "tracked".
func TestInitialShipSteps_KindOmitted(t *testing.T) {
	steps := InitialShipSteps()
	if len(steps) != 6 {
		t.Fatalf("len(InitialShipSteps()) = %d, want 6", len(steps))
	}
	for _, s := range steps {
		if s.Kind != "" {
			t.Errorf("step %q: Kind = %q, want empty (InitialShipSteps must stay byte-shape compatible)", s.Name, s.Kind)
		}
	}

	raw, err := json.Marshal(steps[0])
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, present := m["kind"]; present {
		t.Errorf("marshaled steps[0] = %s, want no \"kind\" key (omitempty)", raw)
	}
}

// TestTrackedShipSteps_MatchesInitialShipSteps proves TrackedShipSteps (the
// set InitialShipStepsFromConfig classifies "tracked") is exactly the name
// set InitialShipSteps' fixed scaffold seeds — the two must never drift
// apart, since they describe the same "has a begin-step/complete-step
// lifecycle" concept from two different entry points.
func TestTrackedShipSteps_MatchesInitialShipSteps(t *testing.T) {
	var fromFixed []string
	for _, s := range InitialShipSteps() {
		fromFixed = append(fromFixed, s.Name)
	}
	if !reflect.DeepEqual(TrackedShipSteps, fromFixed) {
		t.Errorf("TrackedShipSteps = %v, want %v (InitialShipSteps' name order)", TrackedShipSteps, fromFixed)
	}
}

// TestShipStepNameEnum_CoversCanonicalSteps proves
// plugins/sdlc/schemas/ship-state.schema.json's steps[].items.properties.name
// enum is a superset of CanonicalSteps — every step InitialShipStepsFromConfig
// can be asked to seed from a valid ship.steps[] config must validate.
// Enforced here, by test, not by generation (mirrors
// TestMaxWaveTimeoutSecondsMatchesSchema's convention above).
func TestShipStepNameEnum_CoversCanonicalSteps(t *testing.T) {
	schemaPath := filepath.Join("..", "..", "plugins", "sdlc", "schemas", "ship-state.schema.json")
	raw, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatalf("reading %s: %v", schemaPath, err)
	}

	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parsing %s: %v", schemaPath, err)
	}

	props, _ := doc["properties"].(map[string]any)
	stepsProp, _ := props["steps"].(map[string]any)
	items, _ := stepsProp["items"].(map[string]any)
	itemProps, _ := items["properties"].(map[string]any)
	nameProp, _ := itemProps["name"].(map[string]any)
	enumRaw, _ := nameProp["enum"].([]any)
	if len(enumRaw) == 0 {
		t.Fatalf("could not locate steps[].items.properties.name.enum in %s", schemaPath)
	}

	enum := make(map[string]bool, len(enumRaw))
	for _, v := range enumRaw {
		if s, ok := v.(string); ok {
			enum[s] = true
		}
	}

	for _, name := range CanonicalSteps {
		if !enum[name] {
			t.Errorf("CanonicalSteps %q missing from ship-state.schema.json's steps[].name enum: %v", name, enumRaw)
		}
	}
}

// TestShipStateSkipEnum_HasHarden pins "harden" in ship-state.schema.json's
// flags.skip enum, alongside the other inline opt-in steps.
func TestShipStateSkipEnum_HasHarden(t *testing.T) {
	doc := readSchema(t, "ship-state.schema.json")
	props, _ := doc["properties"].(map[string]any)
	flags, _ := props["flags"].(map[string]any)
	flagProps, _ := flags["properties"].(map[string]any)
	enum := itemsEnum(t, flagProps["skip"], "flags.skip")
	if !enum["harden"] {
		t.Errorf("ship-state.schema.json flags.skip enum is missing %q", "harden")
	}
}

// TestLocalSchemaStepEnums_MatchCanonicalSteps proves the ship.steps and
// ship.quick item enums in sdlc-local.schema.json hold exactly the
// CanonicalSteps names — a step missing from either side fails here.
func TestLocalSchemaStepEnums_MatchCanonicalSteps(t *testing.T) {
	doc := readSchema(t, "sdlc-local.schema.json")
	defs, _ := doc["$defs"].(map[string]any)
	ship, _ := defs["shipSection"].(map[string]any)
	shipProps, _ := ship["properties"].(map[string]any)

	want := make(map[string]bool, len(CanonicalSteps))
	for _, s := range CanonicalSteps {
		want[s] = true
	}
	for _, field := range []string{"steps", "quick"} {
		got := itemsEnum(t, shipProps[field], "ship."+field)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("sdlc-local.schema.json ship.%s enum = %v, want CanonicalSteps %v", field, got, CanonicalSteps)
		}
	}
}

// TestLocalTemplate_DefaultStepsIncludeHarden pins the steps line new
// projects receive from plugins/sdlc/templates/local.toml.
func TestLocalTemplate_DefaultStepsIncludeHarden(t *testing.T) {
	path := filepath.Join("..", "..", "plugins", "sdlc", "templates", "local.toml")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	const want = `steps = ["execute", "commit", "review", "harden", "pr", "verify-pipeline"]`
	if !strings.Contains(string(raw), "\n"+want+"\n") {
		t.Errorf("%s: missing default steps line %s", path, want)
	}
}

// readSchema parses plugins/sdlc/schemas/<name> into a generic map.
func readSchema(t *testing.T, name string) map[string]any {
	t.Helper()
	path := filepath.Join("..", "..", "plugins", "sdlc", "schemas", name)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}
	return doc
}

// itemsEnum returns the set of string values in prop.items.enum, failing the
// test when the enum cannot be located.
func itemsEnum(t *testing.T, prop any, label string) map[string]bool {
	t.Helper()
	p, _ := prop.(map[string]any)
	items, _ := p["items"].(map[string]any)
	enumRaw, _ := items["enum"].([]any)
	if len(enumRaw) == 0 {
		t.Fatalf("could not locate %s items.enum", label)
	}
	out := make(map[string]bool, len(enumRaw))
	for _, v := range enumRaw {
		if s, ok := v.(string); ok {
			out[s] = true
		}
	}
	return out
}
