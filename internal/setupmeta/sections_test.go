package setupmeta

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/rnagrodzki/sdlc-plugin/internal/commstyle"
)

// TestVersionFields_PreReleasePolicyMatchesSchema keeps the setup wizard's
// preReleasePolicy field in step with the enum in sdlc-config.schema.json:
// same options in the same order, a default that is one of them, and a
// description that names every option.
func TestVersionFields_PreReleasePolicyMatchesSchema(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "plugins", "sdlc", "schemas", "sdlc-config.schema.json"))
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	var schema struct {
		Defs struct {
			VersionSection struct {
				Properties struct {
					PreReleasePolicy struct {
						Enum []string `json:"enum"`
					} `json:"preReleasePolicy"`
				} `json:"properties"`
			} `json:"versionSection"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatalf("parse schema: %v", err)
	}
	wantOptions := schema.Defs.VersionSection.Properties.PreReleasePolicy.Enum
	if len(wantOptions) == 0 {
		t.Fatal("schema has no $defs.versionSection.properties.preReleasePolicy.enum")
	}

	var field *Field
	for i := range versionFields {
		if versionFields[i].Name == "preReleasePolicy" {
			field = &versionFields[i]
			break
		}
	}
	if field == nil {
		t.Fatal(`versionFields has no "preReleasePolicy" field`)
	}

	if !reflect.DeepEqual(field.Options, wantOptions) {
		t.Errorf("preReleasePolicy Options:\n  got:  %v\n  want: %v (schema enum)", field.Options, wantOptions)
	}
	def, _ := field.Default.(string)
	if !slices.Contains(field.Options, def) {
		t.Errorf("preReleasePolicy Default %v is not one of Options %v", field.Default, field.Options)
	}
	for _, opt := range field.Options {
		if !strings.Contains(field.Description, "`"+opt+"`") {
			t.Errorf("preReleasePolicy Description does not name option %q", opt)
		}
	}
}

// TestCanonicalSteps_Contents pins CanonicalSteps' exact contents and order.
// This slice seeds the ship.steps setup-wizard field's Options and Default
// (ShipFields, below), so a regression here silently changes what step
// values new projects are offered/defaulted to during /setup. In
// particular, "version" must stay absent — it was deliberately removed
// (folded into other steps) and must not resurface as a selectable step.
func TestCanonicalSteps_Contents(t *testing.T) {
	want := []string{
		"execute", "commit", "review", "harden", "verify-openspec",
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

// TestSections_Count pins the total number of setup sections (19, after the
// "communication-style" section was added).
func TestSections_Count(t *testing.T) {
	if got := len(Sections()); got != 19 {
		t.Errorf("Sections() returned %d sections, want 19", got)
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

	if automation.ConfigFile != ".sdlc-v2/local.toml" {
		t.Errorf("automation.ConfigFile = %q, want %q", automation.ConfigFile, ".sdlc-v2/local.toml")
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

// TestPlanStyleFields_InstructionsMatchesSchema keeps the setup wizard's
// planStyleFields "instructions" entry in step with the "instructions"
// property on $defs.planStyleSection in sdlc-local.schema.json: the schema
// property must exist and be a JSON array, and the wizard field must be a
// "list" field with no default.
func TestPlanStyleFields_InstructionsMatchesSchema(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "plugins", "sdlc", "schemas", "sdlc-local.schema.json"))
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	var schema struct {
		Defs struct {
			PlanStyleSection struct {
				Properties struct {
					Instructions struct {
						Type string `json:"type"`
					} `json:"instructions"`
				} `json:"properties"`
			} `json:"planStyleSection"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatalf("parse schema: %v", err)
	}
	wantType := schema.Defs.PlanStyleSection.Properties.Instructions.Type
	if wantType != "array" {
		t.Fatalf("schema $defs.planStyleSection.properties.instructions.type = %q, want %q", wantType, "array")
	}

	var field *Field
	for i := range planStyleFields {
		if planStyleFields[i].Name == "instructions" {
			field = &planStyleFields[i]
			break
		}
	}
	if field == nil {
		t.Fatal(`planStyleFields has no "instructions" field`)
	}
	if field.Type != "list" {
		t.Errorf("instructions Type = %q, want %q", field.Type, "list")
	}
	if field.Default != nil {
		t.Errorf("instructions Default = %v, want nil", field.Default)
	}
}

// enumProp holds a JSON schema property's "enum" array, for decoding one
// property at a time out of sdlc-local.schema.json.
type enumProp struct {
	Enum []string `json:"enum"`
}

// TestPlanStyleEnums_MatchSchema proves the enum lists declared in
// $defs.styleSection and $defs.planStyleSection of sdlc-local.schema.json
// hold exactly the commstyle package's allowed values. A schema enum that
// drifts from commstyle would silently let an editor accept a value the
// runtime rejects, or reject a value the runtime accepts.
func TestPlanStyleEnums_MatchSchema(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "plugins", "sdlc", "schemas", "sdlc-local.schema.json"))
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	var schema struct {
		Defs struct {
			StyleSection struct {
				Properties struct {
					Audience        enumProp `json:"audience"`
					WritingStandard enumProp `json:"writingStandard"`
					Tone            enumProp `json:"tone"`
				} `json:"properties"`
			} `json:"styleSection"`
			PlanStyleSection struct {
				Properties struct {
					Audience        enumProp `json:"audience"`
					WritingStandard enumProp `json:"writingStandard"`
					Tone            enumProp `json:"tone"`
					VisualDensity   enumProp `json:"visualDensity"`
				} `json:"properties"`
			} `json:"planStyleSection"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatalf("parse schema: %v", err)
	}

	cases := []struct {
		label string
		got   []string
		want  []string
	}{
		{"styleSection.audience", schema.Defs.StyleSection.Properties.Audience.Enum, commstyle.Audiences},
		{"styleSection.writingStandard", schema.Defs.StyleSection.Properties.WritingStandard.Enum, commstyle.WritingStandards},
		{"styleSection.tone", schema.Defs.StyleSection.Properties.Tone.Enum, commstyle.Tones},
		{"planStyleSection.audience (deprecated alias)", schema.Defs.PlanStyleSection.Properties.Audience.Enum, commstyle.Audiences},
		{"planStyleSection.writingStandard (deprecated alias)", schema.Defs.PlanStyleSection.Properties.WritingStandard.Enum, commstyle.WritingStandards},
		{"planStyleSection.tone (deprecated alias)", schema.Defs.PlanStyleSection.Properties.Tone.Enum, commstyle.Tones},
		{"planStyleSection.visualDensity", schema.Defs.PlanStyleSection.Properties.VisualDensity.Enum, commstyle.VisualDensities},
	}
	for _, c := range cases {
		if !reflect.DeepEqual(c.got, c.want) {
			t.Errorf("%s enum:\n  got:  %v\n  want: %v (commstyle)", c.label, c.got, c.want)
		}
	}
}

// TestLocalTemplate_StyleValuesMatchEnums parses the commented [style] and
// [planStyle] example lines in plugins/sdlc/templates/local.toml and fails
// when an example value is not one of the commstyle package's allowed
// values for that key — catching drift between the template's example
// comments and the real enum lists (e.g. a leftover "mixed" or "detailed").
func TestLocalTemplate_StyleValuesMatchEnums(t *testing.T) {
	path := filepath.Join("..", "..", "plugins", "sdlc", "templates", "local.toml")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}

	enumsByKey := map[string][]string{
		"audience":        commstyle.Audiences,
		"writingStandard": commstyle.WritingStandards,
		"tone":            commstyle.Tones,
		"visualDensity":   commstyle.VisualDensities,
	}

	lineRe := regexp.MustCompile(`^#?\s*(\w+)\s*=\s*"([^"]*)"`)
	found := map[string]bool{}
	for _, line := range strings.Split(string(raw), "\n") {
		m := lineRe.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		key, value := m[1], m[2]
		allowed, ok := enumsByKey[key]
		if !ok {
			continue
		}
		found[key] = true
		if !slices.Contains(allowed, value) {
			t.Errorf("%s: example %s = %q is not in commstyle's allowed values %v", path, key, value, allowed)
		}
	}
	for key := range enumsByKey {
		if !found[key] {
			t.Errorf("%s: no commented example line found for %q", path, key)
		}
	}
}

// TestCommunicationStyleSection verifies the "communication-style" section
// descriptor: storage location, consuming skills, and that its Fields slice
// is exactly styleFields (the 5 keys shared by every sdlc skill).
func TestCommunicationStyleSection(t *testing.T) {
	var commStyle *Section
	sections := Sections()
	for i := range sections {
		if sections[i].ID == "communication-style" {
			commStyle = &sections[i]
			break
		}
	}
	if commStyle == nil {
		t.Fatal(`Sections() has no "communication-style" section`)
	}

	if commStyle.ConfigFile != ".sdlc-v2/local.toml" {
		t.Errorf("communication-style.ConfigFile = %q, want %q", commStyle.ConfigFile, ".sdlc-v2/local.toml")
	}
	if commStyle.ConfigPath != "style" {
		t.Errorf("communication-style.ConfigPath = %q, want %q", commStyle.ConfigPath, "style")
	}
	if !commStyle.Optional {
		t.Error("communication-style.Optional = false, want true")
	}
	if len(commStyle.Fields) != len(styleFields) {
		t.Fatalf("communication-style.Fields has %d entries, want %d (styleFields)", len(commStyle.Fields), len(styleFields))
	}
	for i := range styleFields {
		if commStyle.Fields[i].Name != styleFields[i].Name {
			t.Errorf("communication-style.Fields[%d].Name = %q, want %q", i, commStyle.Fields[i].Name, styleFields[i].Name)
		}
	}
}

// TestStyleFields_NamesAndOptions pins the DRY split of plan Task 4: the 5
// keys shared by every sdlc skill live in styleFields (backing [style]),
// and the 3 plan-only keys live in planStyleFields (backing [planStyle]).
// It also proves each enum field's Options/Default is the matching
// commstyle value, not a hand-typed copy that can drift from it.
func TestStyleFields_NamesAndOptions(t *testing.T) {
	wantStyleNames := []string{"audience", "writingStandard", "tone", "language", "technicalTerms"}
	gotStyleNames := make([]string, len(styleFields))
	for i, f := range styleFields {
		gotStyleNames[i] = f.Name
	}
	if !reflect.DeepEqual(gotStyleNames, wantStyleNames) {
		t.Errorf("styleFields names:\n  got:  %v\n  want: %v", gotStyleNames, wantStyleNames)
	}

	wantPlanStyleNames := []string{"visualDensity", "narrativeRules", "instructions"}
	gotPlanStyleNames := make([]string, len(planStyleFields))
	for i, f := range planStyleFields {
		gotPlanStyleNames[i] = f.Name
	}
	if !reflect.DeepEqual(gotPlanStyleNames, wantPlanStyleNames) {
		t.Errorf("planStyleFields names:\n  got:  %v\n  want: %v", gotPlanStyleNames, wantPlanStyleNames)
	}
	if len(styleFields) < 4 || len(planStyleFields) < 1 {
		t.Fatal("styleFields or planStyleFields has fewer entries than expected; index-based checks below would be invalid")
	}

	optionChecks := []struct {
		label string
		got   []string
		want  []string
	}{
		{"styleFields[audience].Options", styleFields[0].Options, commstyle.Audiences},
		{"styleFields[writingStandard].Options", styleFields[1].Options, commstyle.WritingStandards},
		{"styleFields[tone].Options", styleFields[2].Options, commstyle.Tones},
		{"planStyleFields[visualDensity].Options", planStyleFields[0].Options, commstyle.VisualDensities},
	}
	for _, c := range optionChecks {
		if !reflect.DeepEqual(c.got, c.want) {
			t.Errorf("%s:\n  got:  %v\n  want: %v (commstyle)", c.label, c.got, c.want)
		}
	}

	defaultChecks := []struct {
		label string
		got   any
		want  any
	}{
		{"styleFields[audience].Default", styleFields[0].Default, commstyle.DefaultAudience},
		{"styleFields[writingStandard].Default", styleFields[1].Default, commstyle.DefaultWritingStandard},
		{"styleFields[tone].Default", styleFields[2].Default, commstyle.DefaultTone},
		{"styleFields[language].Default", styleFields[3].Default, commstyle.DefaultLanguage},
		{"planStyleFields[visualDensity].Default", planStyleFields[0].Default, commstyle.DefaultVisualDensity},
	}
	for _, c := range defaultChecks {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v (commstyle)", c.label, c.got, c.want)
		}
	}
}
