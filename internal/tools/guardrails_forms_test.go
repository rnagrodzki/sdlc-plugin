package tools

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
)

// TestLoadSurfaceGuardrails_BothTomlForms verifies plan.guardrails and
// execute.guardrails load the same way from the named-table form
// ([<section>.guardrails.<id>], id from the key, sorted by id) and the
// array-of-tables form ([[<section>.guardrails]], id from the entry, file
// order kept). validate accepts both forms, so the readers must too.
func TestLoadSurfaceGuardrails_BothTomlForms(t *testing.T) {
	for _, section := range []string{"plan", "execute"} {
		t.Run(section+" named tables", func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, filepath.Join(dir, paths.DataDir, "config.toml"), ""+
				"["+section+".guardrails.b-rule]\n"+
				"severity = \"warning\"\n"+
				"description = \"B\"\n"+
				"\n"+
				"["+section+".guardrails.a-rule]\n"+
				"severity = \"error\"\n"+
				"description = \"A\"\n")
			var errs []surfaceLoadError
			got := loadSurfaceGuardrails(dir, section, &errs)
			want := []surfaceGuardrail{
				{ID: "a-rule", Severity: "error", Description: "A"},
				{ID: "b-rule", Severity: "warning", Description: "B"},
			}
			if len(errs) != 0 || !reflect.DeepEqual(got, want) {
				t.Errorf("got %+v errs %+v, want %+v", got, errs, want)
			}
		})
		t.Run(section+" array of tables", func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, filepath.Join(dir, paths.DataDir, "config.toml"), ""+
				"[["+section+".guardrails]]\n"+
				"id = \"b-rule\"\n"+
				"severity = \"warning\"\n"+
				"description = \"B\"\n"+
				"\n"+
				"[["+section+".guardrails]]\n"+
				"id = \"a-rule\"\n"+
				"severity = \"error\"\n"+
				"description = \"A\"\n")
			var errs []surfaceLoadError
			got := loadSurfaceGuardrails(dir, section, &errs)
			want := []surfaceGuardrail{
				{ID: "b-rule", Severity: "warning", Description: "B"},
				{ID: "a-rule", Severity: "error", Description: "A"},
			}
			if len(errs) != 0 || !reflect.DeepEqual(got, want) {
				t.Errorf("got %+v errs %+v, want %+v", got, errs, want)
			}
		})
	}
}
