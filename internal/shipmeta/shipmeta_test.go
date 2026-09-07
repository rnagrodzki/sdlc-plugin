package shipmeta

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/rnagrodzki/sdlc-plugin/internal/state"
)

// TestMaxWaveTimeoutSecondsMatchesSchema proves the single enforcement point
// (Acceptance Criterion 1): MaxWaveTimeoutSeconds must equal the `maximum`
// on ship.executeWaveTimeout in plugins/sdlc/schemas/sdlc-local.schema.json. The schema
// cross-reference is enforced here, by test, not by code generation (see
// Task 19 fact sheet decisions).
func TestMaxWaveTimeoutSecondsMatchesSchema(t *testing.T) {
	schemaPath := filepath.Join("..", "..", "plugins", "sdlc", "schemas", "sdlc-local.schema.json")
	raw, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatalf("reading %s: %v", schemaPath, err)
	}

	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parsing %s: %v", schemaPath, err)
	}

	defs, ok := doc["$defs"].(map[string]any)
	if !ok {
		t.Fatalf("%s: missing $defs object", schemaPath)
	}
	shipSection, ok := defs["shipSection"].(map[string]any)
	if !ok {
		t.Fatalf("%s: missing $defs.shipSection object", schemaPath)
	}
	props, ok := shipSection["properties"].(map[string]any)
	if !ok {
		t.Fatalf("%s: missing $defs.shipSection.properties object", schemaPath)
	}
	executeWaveTimeout, ok := props["executeWaveTimeout"].(map[string]any)
	if !ok {
		t.Fatalf("%s: missing $defs.shipSection.properties.executeWaveTimeout object", schemaPath)
	}
	maxRaw, ok := executeWaveTimeout["maximum"]
	if !ok {
		t.Fatalf("%s: executeWaveTimeout is missing a maximum", schemaPath)
	}
	maxFloat, ok := maxRaw.(float64)
	if !ok {
		t.Fatalf("%s: executeWaveTimeout.maximum is not a number: %#v", schemaPath, maxRaw)
	}

	if got, want := int(maxFloat), MaxWaveTimeoutSeconds; got != want {
		t.Errorf("schema executeWaveTimeout.maximum = %d, want MaxWaveTimeoutSeconds = %d", got, want)
	}
}

// TestSubstepMapMatchesSource proves Acceptance Criterion 2: SubstepMap
// renders the same todo lists as scripts/lib/ship-todos.js SUBSTEP_MAP, for
// every pipeline step fixture. Values below are copied verbatim from the
// source SUBSTEP_MAP (F-shared-lib-cross-cutting-behavior-77/78), except
// "learnings-commit": the source's "commit log" substep was dropped when the
// step was redefined as append-only (learnings/log.md is gitignored and
// never committed — see internal/tools/learnings.go).
func TestSubstepMapMatchesSource(t *testing.T) {
	tests := []struct {
		step string
		want []string
	}{
		{"execute", []string{"execute plan"}},
		{"commit", []string{"stash unstaged", "generate message", "commit", "restore stash"}},
		{"review", []string{"dispatch review dimensions", "collect verdicts"}},
		{"received-review", []string{"fetch comments", "classify findings", "apply auto-fixes", "surface remaining"}},
		{"commit-fixes", []string{"re-stage", "commit fixes"}},
		{"version", []string{"bump version", "update CHANGELOG", "tag"}},
		{"verify-openspec", []string{"openspec validate --strict", "check result"}},
		{"archive-openspec", []string{"validate", "run archive", "stage", "commit"}},
		{"pr", []string{"push branch", "draft body", "gh pr create", "apply labels"}},
		{"verify-pipeline", []string{"poll checks", "fetch logs on failure", "analyze", "commit fix if any"}},
		{"await-remote-review", []string{"poll reviews", "dispatch received-review if actionable", "commit fix if any"}},
		{"learnings-commit", []string{"append log"}},
		{"cleanup", []string{"cleanup pipeline state"}},
	}

	if len(SubstepMap) != len(tests) {
		t.Errorf("SubstepMap has %d entries, source fixture has %d", len(SubstepMap), len(tests))
	}

	for _, tt := range tests {
		t.Run(tt.step, func(t *testing.T) {
			got, ok := SubstepMap[tt.step]
			if !ok {
				t.Fatalf("SubstepMap missing step %q", tt.step)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("SubstepMap[%q] = %#v, want %#v", tt.step, got, tt.want)
			}
		})
	}
}

// TestTodosForStepMatchesSource proves TodosForStep matches
// stepTransition(state, "version").todos from scripts/lib/ship-todos.js for a
// fixture with mixed completed/skipped/failed/pending/current step statuses.
// The want slice below was produced by running the actual JS reference:
//
//	node -e '
//	  const { stepTransition } = require("scripts/lib/ship-todos.js");
//	  const state = {
//	    flags: { steps: ["execute", "commit", "review", "version", "pr"] },
//	    steps: [
//	      { name: "execute", status: "completed" },
//	      { name: "commit",  status: "skipped" },
//	      { name: "review",  status: "failed" },
//	      { name: "version", status: "in_progress" },
//	      { name: "pr",      status: "pending" },
//	    ],
//	  };
//	  console.log(JSON.stringify(stepTransition(state, "version").todos));
//	'
func TestTodosForStepMatchesSource(t *testing.T) {
	st := &state.State{
		Data: map[string]any{
			"flags": map[string]any{
				"steps": []any{"execute", "commit", "review", "version", "pr"},
			},
			"steps": []any{
				map[string]any{"name": "execute", "status": "completed"},
				map[string]any{"name": "commit", "status": "skipped"},
				map[string]any{"name": "review", "status": "failed"},
				map[string]any{"name": "version", "status": "in_progress"},
				map[string]any{"name": "pr", "status": "pending"},
			},
		},
	}

	want := []Todo{
		{Content: "Execute: execute plan", ActiveForm: "Execute plan", Status: "completed"},
		{Content: "Commit: stash unstaged (skipped)", ActiveForm: "Stash unstaged", Status: "completed"},
		{Content: "Commit: generate message (skipped)", ActiveForm: "Generate message", Status: "completed"},
		{Content: "Commit: commit (skipped)", ActiveForm: "Commit", Status: "completed"},
		{Content: "Commit: restore stash (skipped)", ActiveForm: "Restore stash", Status: "completed"},
		{Content: "Review: dispatch review dimensions (failed)", ActiveForm: "Dispatch review dimensions", Status: "completed"},
		{Content: "Review: collect verdicts (failed)", ActiveForm: "Collect verdicts", Status: "completed"},
		{Content: "Version: bump version", ActiveForm: "Bump version", Status: "in_progress"},
		{Content: "Version: update CHANGELOG", ActiveForm: "Update CHANGELOG", Status: "pending"},
		{Content: "Version: tag", ActiveForm: "Tag", Status: "pending"},
		{Content: "Pr: push branch", ActiveForm: "Push branch", Status: "pending"},
		{Content: "Pr: draft body", ActiveForm: "Draft body", Status: "pending"},
		{Content: "Pr: gh pr create", ActiveForm: "Gh pr create", Status: "pending"},
		{Content: "Pr: apply labels", ActiveForm: "Apply labels", Status: "pending"},
		{Content: "Cleanup: cleanup pipeline state", ActiveForm: "Cleanup pipeline state", Status: "pending"},
	}

	got := TodosForStep("version", st)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("TodosForStep(\"version\", st) =\n%#v\nwant\n%#v", got, want)
	}
}
