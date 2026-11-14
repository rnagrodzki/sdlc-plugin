//go:build integration

package config

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"

	version "github.com/rnagrodzki/sdlc-plugin"
	"github.com/rnagrodzki/sdlc-plugin/internal/fsx"
)

// instructionsBlockStart marks the start of the follow-up instructions
// block in both templates. The block ends at the next "# ───" divider or at
// the end of the file.
const instructionsBlockStart = "# ─── Follow-up instructions"

// instructionsTOMLLine matches a live or commented TOML line of the
// instructions block: a [instructions] or [[instructions.<key>]] header, or
// a "<name> =" line. Prose comment lines do not match.
var instructionsTOMLLine = regexp.MustCompile(`^#?\s*(\[\[?instructions[.\]]|[A-Za-z][A-Za-z-]*\s*=)`)

// templateInstructionExamples returns the TOML documents of the commented
// examples in the instructions block of template: one document of every
// header and key line, and one document for each inline-table line
// ("instructions = {...}"), which cannot share a document with the headers.
func templateInstructionExamples(t *testing.T, name, template string) []string {
	t.Helper()
	start := strings.Index(template, instructionsBlockStart)
	if start < 0 {
		t.Fatalf("%s: no %q block", name, instructionsBlockStart)
	}
	block := template[start+len(instructionsBlockStart):]
	if end := strings.Index(block, "# ───"); end >= 0 {
		block = block[:end]
	}

	var main []string
	var docs []string
	for _, line := range strings.Split(block, "\n") {
		trimmed := strings.TrimSpace(line)
		if !instructionsTOMLLine.MatchString(trimmed) {
			continue
		}
		uncommented := strings.TrimSpace(strings.TrimPrefix(trimmed, "#"))
		if strings.HasPrefix(uncommented, "instructions") && strings.Contains(uncommented, "=") && !strings.HasPrefix(uncommented, "[") {
			docs = append(docs, uncommented+"\n")
			continue
		}
		main = append(main, uncommented)
	}
	if len(main) == 0 {
		t.Fatalf("%s: the instructions block has no example lines", name)
	}
	return append([]string{strings.Join(main, "\n") + "\n"}, docs...)
}

// TestInstructionTemplates checks each example of the instructions block of
// both templates against both schemas and against ReadInstructions, and
// checks that each full template loads with no error.
func TestInstructionTemplates(t *testing.T) {
	Quiet = true
	defer func() { Quiet = false }()

	cases := []struct {
		name     string
		template string
		schema   string
		file     string // the file the template becomes
	}{
		{"config.toml", version.ConfigTemplate, "sdlc-config.schema.json", "config.toml"},
		{"local.toml", version.LocalTemplate, "sdlc-local.schema.json", "local.toml"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			abs, err := filepath.Abs(filepath.Join("..", "..", "plugins", "sdlc", "schemas", tc.schema))
			if err != nil {
				t.Fatal(err)
			}
			sch, err := jsonschema.NewCompiler().Compile(abs)
			if err != nil {
				t.Fatalf("compile %s: %v", tc.schema, err)
			}

			examples := templateInstructionExamples(t, tc.name, tc.template)
			if len(examples) < 2 {
				t.Fatalf("got %d example documents, want the header form and the inline form", len(examples))
			}
			for i, doc := range examples {
				var raw map[string]any
				if err := fsx.DecodeTOML([]byte(doc), &raw); err != nil {
					t.Fatalf("example %d does not decode: %v\n%s", i, err, doc)
				}
				if err := sch.Validate(raw); err != nil {
					t.Errorf("example %d does not validate against %s: %v\n%s", i, tc.schema, err, doc)
				}

				root := t.TempDir()
				writeInstructionsFile(t, filepath.Join(root, ".sdlc-v2", tc.file), doc)
				got, err := ReadInstructions(root)
				if err != nil {
					t.Errorf("example %d: ReadInstructions: %v\n%s", i, err, doc)
					continue
				}
				if len(got) == 0 {
					t.Errorf("example %d: ReadInstructions returned no instruction\n%s", i, doc)
				}
			}

			// The full template, as /setup writes it, loads with no instruction.
			root := t.TempDir()
			writeInstructionsFile(t, filepath.Join(root, ".sdlc-v2", tc.file), tc.template)
			got, err := ReadInstructions(root)
			if err != nil {
				t.Fatalf("full template: ReadInstructions: %v", err)
			}
			if len(got) != 0 {
				t.Errorf("full template: got %v, want no instruction (every example is commented)", got)
			}
		})
	}

}
