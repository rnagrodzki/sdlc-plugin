//go:build integration

package tools

import (
	"os"
	"strings"
	"testing"
)

// TestIntegration_SetupWriteSections_NullDeletesGuardrail calls the registered
// setup_write_sections tool with a JSON null on a plan guardrail id and on an
// execute guardrail id. Each call must remove that table from config.toml and
// leave every other byte of the file as it was. The output lists the id
// under deleted, with the removed data, and not under written.
func TestIntegration_SetupWriteSections_NullDeletesGuardrail(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		sections string
		written  string
		want     string
	}{
		{
			name:     "plan guardrail keeps its sibling and the jira section",
			content:  guardrailsFile,
			sections: `{"plan.guardrails.dry-old":null}`,
			written:  "plan.guardrails.dry-old",
			want: "[plan.guardrails.dry]\ndescription = \"No duplicate logic.\"\nseverity = \"warning\"\n\n" +
				"[jira]\ndefaultProject = \"P\"\n",
		},
		{
			name:     "execute guardrail keeps its sibling",
			content:  "[execute.guardrails.a]\nseverity = \"error\"\n\n[execute.guardrails.b]\nseverity = \"warning\"\n",
			sections: `{"execute.guardrails.a":null}`,
			written:  "execute.guardrails.a",
			want:     "[execute.guardrails.b]\nseverity = \"warning\"\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := writeSDLCFile(t, dir, "config.toml", tt.content)

			res, text := callRegisteredSetupWriteSectionsIn(t, dir, tt.sections)
			if res.IsError {
				t.Fatalf("expected success, got tool error:\n%s", text)
			}
			for _, want := range []string{"- ok: true", "- written: (none)", "## deleted[0]\n- id: " + tt.written + "\n"} {
				if !strings.Contains(text, want) {
					t.Errorf("output missing %q:\n%s", want, text)
				}
			}
			if strings.Contains(text, "not present, nothing deleted") {
				t.Errorf("output reports a missing table for an id that exists:\n%s", text)
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tt.want {
				t.Errorf("unexpected file.\n--- got ---\n%s\n--- want ---\n%s", got, tt.want)
			}
		})
	}
}

// TestIntegration_SetupWriteSections_NullGuardrailWithCommentKeepsFile calls
// the registered setup_write_sections tool with a null on a guardrail whose
// block holds a comment line, together with a write of another section. The
// delete is refused and config.toml keeps the commented block, while the other
// section is written. The output reports the refusal and the hand-edit instruction.
func TestIntegration_SetupWriteSections_NullGuardrailWithCommentKeepsFile(t *testing.T) {
	dir := t.TempDir()
	block := "[plan.guardrails.dry]\n# keep this note\nseverity = \"warning\"\n"
	path := writeSDLCFile(t, dir, "config.toml", block+"\n[jira]\ndefaultProject = \"P\"\n")

	res, text := callRegisteredSetupWriteSectionsIn(t, dir,
		`{"commit":{"style":"conventional"},"plan.guardrails.dry":null}`)
	if res.IsError {
		t.Fatalf("expected a tool success carrying a per-section error, got tool error:\n%s", text)
	}
	for _, want := range []string{
		"- ok: false",
		"section plan.guardrails.dry:",
		"  - commit",
		"Edit section plan.guardrails.dry in .sdlc-v2/config.toml by hand, then run setup again for that section. Other sections were written.",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("output missing %q:\n%s", want, text)
		}
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), block) {
		t.Errorf("the commented guardrail block did not survive:\n%s", got)
	}
	if !strings.Contains(string(got), "style = 'conventional'") {
		t.Errorf("the other section (commit) was not written:\n%s", got)
	}
}

// TestIntegration_SetupWriteSections_NullOnMissingGuardrail calls the
// registered setup_write_sections tool with a null on a guardrail id that
// config.toml does not hold. The call succeeds, writes nothing, and the output
// names the id in the warnings and in next.
func TestIntegration_SetupWriteSections_NullOnMissingGuardrail(t *testing.T) {
	dir := t.TempDir()
	path := writeSDLCFile(t, dir, "config.toml", guardrailsFile)

	res, text := callRegisteredSetupWriteSectionsIn(t, dir, `{"plan.guardrails.x":null}`)
	if res.IsError {
		t.Fatalf("expected success, got tool error:\n%s", text)
	}
	for _, want := range []string{
		"- ok: true",
		"plan.guardrails.x: not present, nothing deleted",
		"Nothing was deleted for plan.guardrails.x: no such guardrail table.",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("output missing %q:\n%s", want, text)
		}
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != guardrailsFile {
		t.Errorf("file changed:\n%s", got)
	}
}

// TestIntegration_SetupWriteSections_WholeTableDropRefused calls the registered
// setup_write_sections tool with a whole-table key that drops a guardrail id
// that config.toml holds. The call is refused with a Do this section, and
// config.toml stays as it was. The one-id key for the same table then
// succeeds and keeps the other id.
func TestIntegration_SetupWriteSections_WholeTableDropRefused(t *testing.T) {
	dir := t.TempDir()
	const before = "[execute.guardrails.a]\nseverity = \"error\"\n\n[execute.guardrails.b]\nseverity = \"warning\"\n"
	path := writeSDLCFile(t, dir, "config.toml", before)

	res, text := callRegisteredSetupWriteSectionsIn(t, dir,
		`{"execute.guardrails":{"a":{"severity":"error","description":"d"}}}`)
	if !res.IsError {
		t.Fatalf("expected a tool error, got success:\n%s", text)
	}
	for _, want := range []string{
		`key "execute.guardrails" would remove guardrails [b] from execute.guardrails`,
		"## Do this",
		"<section>.guardrails.<id>",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("refusal missing %q:\n%s", want, text)
		}
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != before {
		t.Errorf("config.toml changed after a refusal:\n%s", got)
	}

	res, text = callRegisteredSetupWriteSectionsIn(t, dir,
		`{"execute.guardrails.a":{"severity":"warning","description":"d"}}`)
	if res.IsError {
		t.Fatalf("expected success for the one-id key, got tool error:\n%s", text)
	}
	got, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"[execute.guardrails.a]", "description = 'd'", "[execute.guardrails.b]\nseverity = \"warning\"\n"} {
		if !strings.Contains(string(got), want) {
			t.Errorf("config.toml missing %q after the one-id write:\n%s", want, got)
		}
	}
}
