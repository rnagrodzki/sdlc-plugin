package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/rnagrodzki/sdlc-plugin/internal/shipmeta"
)

// instructionsRoot makes a main worktree root with the given team and
// project local file contents. An empty string writes no file.
func instructionsRoot(t *testing.T, team, local string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".sdlc-v2"), 0o755); err != nil {
		t.Fatal(err)
	}
	if team != "" {
		writeInstructionsFile(t, filepath.Join(root, ".sdlc-v2", "config.toml"), team)
	}
	if local != "" {
		writeInstructionsFile(t, filepath.Join(root, ".sdlc-v2", "local.toml"), local)
	}
	return root
}

func writeInstructionsFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// userInstructionsFile writes content to a user-level file and points
// $SDLC_USER_CONFIG at it for the test.
func userInstructionsFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "user-local.toml")
	writeInstructionsFile(t, path, content)
	t.Setenv(UserConfigPathEnv, path)
	return path
}

// instructionLine renders one instruction as "pr #2 (team) b" for compact
// assertions.
func instructionLine(in Instruction) string {
	return in.Key + " #" + strconv.Itoa(in.Number) + " (" + in.Source + ") " + in.Text
}

func instructionLines(list []Instruction) []string {
	out := make([]string, 0, len(list))
	for _, in := range list {
		out = append(out, instructionLine(in))
	}
	return out
}

func mustReadInstructions(t *testing.T, root string) map[string][]Instruction {
	t.Helper()
	got, err := ReadInstructions(root)
	if err != nil {
		t.Fatalf("ReadInstructions: %v", err)
	}
	return got
}

func mustInstructionsError(t *testing.T, err error) *InstructionsError {
	t.Helper()
	if err == nil {
		t.Fatal("ReadInstructions returned no error")
	}
	var ie *InstructionsError
	if !errors.As(err, &ie) {
		t.Fatalf("error is %T, not *InstructionsError: %v", err, err)
	}
	return ie
}

func TestReadInstructions_MergeOrderAndNumbers(t *testing.T) {
	root := instructionsRoot(t,
		"[[instructions.pr]]\ntext = [\"a\", \"b\"]\n",
		"[[instructions.pr]]\ntext = \"c\"\n",
	)
	userInstructionsFile(t, "[[instructions.pr]]\ntext = \"hidden\"\n\n[[instructions.ship]]\ntext = \"u\"\n")

	got := mustReadInstructions(t, root)

	want := []string{"pr #1 (team) a", "pr #2 (team) b", "pr #3 (project local) c"}
	if lines := instructionLines(got["pr"]); !reflect.DeepEqual(lines, want) {
		t.Errorf("pr = %v, want %v", lines, want)
	}
	wantShip := []string{"ship #1 (user) u"}
	if lines := instructionLines(got["ship"]); !reflect.DeepEqual(lines, wantShip) {
		t.Errorf("ship = %v, want %v", lines, wantShip)
	}
	if len(got) != 2 {
		t.Errorf("got %d keys, want 2 (a key with no instruction is absent): %v", len(got), got)
	}
}

func TestReadInstructions_TeamThenUserWhenLocalDoesNotSetKey(t *testing.T) {
	root := instructionsRoot(t, "[[instructions.commit]]\ntext = \"t\"\n", "[[instructions.pr]]\ntext = \"p\"\n")
	userInstructionsFile(t, "[[instructions.commit]]\ntext = \"u\"\n")

	got := mustReadInstructions(t, root)
	want := []string{"commit #1 (team) t", "commit #2 (user) u"}
	if lines := instructionLines(got["commit"]); !reflect.DeepEqual(lines, want) {
		t.Errorf("commit = %v, want %v", lines, want)
	}
}

func TestReadInstructions_EmptyLocalListHidesUserList(t *testing.T) {
	root := instructionsRoot(t, "", "[instructions]\nharden = []\n")
	userInstructionsFile(t, "[[instructions.harden]]\ntext = \"u\"\n")

	got := mustReadInstructions(t, root)
	if _, ok := got["harden"]; ok {
		t.Errorf("harden = %v, want absent: the empty project local list hides the user list", got["harden"])
	}
}

func TestReadInstructions_HiddenUserListIsStillChecked(t *testing.T) {
	root := instructionsRoot(t, "", "[instructions]\npr = []\n")
	userPath := userInstructionsFile(t, "[[instructions.pr]]\ntext = \"u\"\ncolor = \"red\"\n")

	ie := mustInstructionsError(t, func() error { _, err := ReadInstructions(root); return err }())
	if ie.File != userPath || ie.Field != "instructions.pr[1].color" {
		t.Errorf("error = %+v, want file %s and field instructions.pr[1].color", ie, userPath)
	}
}

func TestReadInstructions_EntryFlags(t *testing.T) {
	root := instructionsRoot(t, `
[[instructions.pr]]
text = ["one", "two"]
normalQuestions = true
criticalQuestions = true
onQuestionNotAllowed = "decide"

[[instructions.pr]]
text = "  three  "
`, "")

	got := mustReadInstructions(t, root)["pr"]
	want := []Instruction{
		{Key: "pr", Number: 1, Source: "team", Text: "one", NormalQuestions: true, CriticalQuestions: true, OnQuestionNotAllowed: "decide"},
		{Key: "pr", Number: 2, Source: "team", Text: "two", NormalQuestions: true, CriticalQuestions: true, OnQuestionNotAllowed: "decide"},
		{Key: "pr", Number: 3, Source: "team", Text: "three", OnQuestionNotAllowed: "skip"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("pr =\n%+v\nwant\n%+v", got, want)
	}
}

func TestReadInstructions_Forms(t *testing.T) {
	cases := []struct {
		name string
		toml string
		want []string
	}{
		{"array-of-tables form", "[[instructions.pr]]\ntext = \"a\"\n", []string{"pr #1 (team) a"}},
		{"inline table form", "instructions = { pr = [ { text = \"a\" } ] }\n", []string{"pr #1 (team) a"}},
		{"nested table form", "[instructions]\npr = [ { text = \"a\" } ]\n", []string{"pr #1 (team) a"}},
		{"empty file", "", nil},
		{"section with no key", "[instructions]\n", nil},
		{"no section", "[jira]\nprojectKey = \"X\"\n", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := instructionsRoot(t, "", "")
			writeInstructionsFile(t, filepath.Join(root, ".sdlc-v2", "config.toml"), tc.toml)
			got := mustReadInstructions(t, root)
			if lines := instructionLines(got["pr"]); !reflect.DeepEqual(lines, nonNil(tc.want)) {
				t.Errorf("pr = %v, want %v", lines, tc.want)
			}
		})
	}
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func TestReadInstructions_MissingFiles(t *testing.T) {
	root := t.TempDir() // no .sdlc-v2 folder at all
	got := mustReadInstructions(t, root)
	if len(got) != 0 {
		t.Errorf("got %v, want an empty map", got)
	}
}

func TestReadInstructions_Errors(t *testing.T) {
	keyList := strings.Join(InstructionKeys, ", ")
	cases := []struct {
		name    string
		toml    string
		field   string
		problem string
		fix     string
	}{
		{"bad TOML", "[instructions\n", "(whole file)", "", "fix the file."},
		{"section not a table", "instructions = 3\n", "instructions", "not a table", "use [[instructions.<key>]] entries."},
		{"unknown key", "[[instructions.deploy]]\ntext = \"a\"\n", "instructions.deploy",
			`"deploy" is not a ship step name and not "ship".`, "use one of: " + keyList + "."},
		{"key not a list", "[instructions]\npr = \"a\"\n", "instructions.pr", "not an array of tables", "use [[instructions.pr]] headers."},
		{"key list of strings", "[instructions]\npr = [\"a\"]\n", "instructions.pr", "not an array of tables", "use [[instructions.pr]] headers."},
		{"text absent", "[[instructions.pr]]\nnormalQuestions = true\n", "instructions.pr[1].text", "missing", "set a non-blank string or a non-empty array of strings."},
		{"text blank", "[[instructions.pr]]\ntext = \"  \"\n", "instructions.pr[1].text", "blank", "set a non-blank string"},
		{"text empty array", "[[instructions.pr]]\ntext = []\n", "instructions.pr[1].text", "empty array", "set a non-blank string"},
		{"text blank item", "[[instructions.pr]]\ntext = [\"a\", \" \"]\n", "instructions.pr[1].text", "item 2: the text is blank.", "set a non-blank string"},
		{"text item not string", "[[instructions.pr]]\ntext = [\"a\", 1]\n", "instructions.pr[1].text", "item 2: the item is not a string.", "set a non-blank string"},
		{"text other type", "[[instructions.pr]]\ntext = 1\n", "instructions.pr[1].text", "not a string or an array of strings", "set a non-blank string"},
		{"text too long", "[[instructions.pr]]\ntext = \"" + strings.Repeat("a", MaxInstructionText+1) + "\"\n", "instructions.pr[1].text",
			"the text has 4001 characters. The limit is 4000.", "shorten the text or split it into 2 instructions."},
		{"normalQuestions not bool", "[[instructions.pr]]\ntext = \"a\"\nnormalQuestions = \"yes\"\n", "instructions.pr[1].normalQuestions", "not a boolean", "use true or false."},
		{"criticalQuestions not bool", "[[instructions.pr]]\ntext = \"a\"\ncriticalQuestions = 1\n", "instructions.pr[1].criticalQuestions", "not a boolean", "use true or false."},
		{"onQuestionNotAllowed bad", "[[instructions.pr]]\ntext = \"a\"\n\n[[instructions.pr]]\ntext = \"b\"\nonQuestionNotAllowed = \"ask\"\n",
			"instructions.pr[2].onQuestionNotAllowed", `"ask" is not "skip" or "decide"`, `use "skip" or "decide".`},
		{"unknown field", "[[instructions.pr]]\ntext = \"a\"\ncolor = \"red\"\n", "instructions.pr[1].color", `"color" is not a known field.`,
			"remove it. Allowed: text, normalQuestions, criticalQuestions, onQuestionNotAllowed."},
		{"list over cap", strings.Repeat("[[instructions.pr]]\ntext = \"a\"\n", MaxInstructionsPerKey+1), "instructions.pr",
			"the list has 51 instructions after text expansion. The limit is 50.", "split or shorten the list."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := instructionsRoot(t, tc.toml, "")
			_, err := ReadInstructions(root)
			ie := mustInstructionsError(t, err)
			if ie.Kind != "data" {
				t.Errorf("Kind = %q, want data", ie.Kind)
			}
			if ie.File != ".sdlc-v2/config.toml" {
				t.Errorf("File = %q, want .sdlc-v2/config.toml", ie.File)
			}
			if ie.Field != tc.field {
				t.Errorf("Field = %q, want %q", ie.Field, tc.field)
			}
			if ie.Problem == "" || !strings.Contains(ie.Problem, tc.problem) {
				t.Errorf("Problem = %q, want it to contain %q", ie.Problem, tc.problem)
			}
			if !strings.Contains(ie.Fix, tc.fix) {
				t.Errorf("Fix = %q, want it to contain %q", ie.Fix, tc.fix)
			}
		})
	}
}

func TestReadInstructions_FirstErrorInFileOrder(t *testing.T) {
	root := instructionsRoot(t, "[[instructions.pr]]\ntext = 1\n", "[[instructions.pr]]\ntext = 2\n")
	ie := mustInstructionsError(t, func() error { _, err := ReadInstructions(root); return err }())
	if ie.File != ".sdlc-v2/config.toml" {
		t.Errorf("File = %q, want the team file first", ie.File)
	}

	root = instructionsRoot(t, "", "[[instructions.pr]]\ntext = 2\n")
	userInstructionsFile(t, "[[instructions.pr]]\ntext = 3\n")
	ie = mustInstructionsError(t, func() error { _, err := ReadInstructions(root); return err }())
	if ie.File != ".sdlc-v2/local.toml" {
		t.Errorf("File = %q, want the project local file before the user file", ie.File)
	}
}

func TestReadInstructions_ListCap(t *testing.T) {
	// The cap counts after text expansion: 25 entries of 2 strings pass.
	pass := strings.Repeat("[[instructions.pr]]\ntext = [\"a\", \"b\"]\n", MaxInstructionsPerKey/2)
	got := mustReadInstructions(t, instructionsRoot(t, pass, ""))
	if n := len(got["pr"]); n != MaxInstructionsPerKey {
		t.Errorf("pr has %d instructions, want %d", n, MaxInstructionsPerKey)
	}

	fail := pass + "[[instructions.pr]]\ntext = \"c\"\n"
	_, err := ReadInstructions(instructionsRoot(t, fail, ""))
	if ie := mustInstructionsError(t, err); ie.Field != "instructions.pr" {
		t.Errorf("Field = %q, want instructions.pr", ie.Field)
	}

	// The cap holds for each file, not for the merged list.
	merged := mustReadInstructions(t, instructionsRoot(t, pass, pass))
	if n := len(merged["pr"]); n != 2*MaxInstructionsPerKey {
		t.Errorf("merged pr has %d instructions, want %d", n, 2*MaxInstructionsPerKey)
	}
}

func TestReadInstructions_TextCap(t *testing.T) {
	// The cap counts characters, not bytes.
	atCap := strings.Repeat("é", MaxInstructionText)
	got := mustReadInstructions(t, instructionsRoot(t, "[[instructions.pr]]\ntext = \""+atCap+"\"\n", ""))
	if len(got["pr"]) != 1 {
		t.Fatalf("pr = %v, want 1 instruction", got["pr"])
	}

	over := atCap + "é"
	_, err := ReadInstructions(instructionsRoot(t, "[[instructions.pr]]\ntext = [\"a\", \""+over+"\"]\n", ""))
	ie := mustInstructionsError(t, err)
	if !strings.Contains(ie.Problem, "item 2: the text has 4001 characters. The limit is 4000.") {
		t.Errorf("Problem = %q", ie.Problem)
	}
}

func TestReadInstructions_FileCap(t *testing.T) {
	sizedFile := func(size int) string {
		head := "[[instructions.pr]]\ntext = \"a\"\n# "
		return head + strings.Repeat("x", size-len(head)-1) + "\n"
	}

	atCap := sizedFile(MaxInstructionsConfigBytes)
	if len(atCap) != MaxInstructionsConfigBytes {
		t.Fatalf("test file has %d bytes", len(atCap))
	}
	got := mustReadInstructions(t, instructionsRoot(t, atCap, ""))
	if len(got["pr"]) != 1 {
		t.Errorf("pr = %v, want 1 instruction", got["pr"])
	}

	_, err := ReadInstructions(instructionsRoot(t, "", sizedFile(MaxInstructionsConfigBytes+1)))
	ie := mustInstructionsError(t, err)
	if ie.Kind != "data" || ie.File != ".sdlc-v2/local.toml" || ie.Field != "(whole file)" {
		t.Errorf("error = %+v", ie)
	}
	if ie.Problem != "the file has 1048577 bytes. The limit is 1048576." || ie.Fix != "shorten the file." {
		t.Errorf("Problem = %q, Fix = %q", ie.Problem, ie.Fix)
	}
}

func TestReadInstructions_UserPath(t *testing.T) {
	t.Run("SDLC_USER_CONFIG", func(t *testing.T) {
		root := instructionsRoot(t, "", "")
		userInstructionsFile(t, "[[instructions.ship]]\ntext = \"u\"\n")
		got := mustReadInstructions(t, root)
		if lines := instructionLines(got["ship"]); !reflect.DeepEqual(lines, []string{"ship #1 (user) u"}) {
			t.Errorf("ship = %v", lines)
		}
	})

	t.Run("home path", func(t *testing.T) {
		root := instructionsRoot(t, "", "")
		home := t.TempDir()
		t.Setenv(UserConfigPathEnv, "")
		t.Setenv("HOME", home)
		writeInstructionsFile(t, filepath.Join(home, ".sdlc", "local.toml"), "[[instructions.ship]]\ntext = \"h\"\n")
		got := mustReadInstructions(t, root)
		if lines := instructionLines(got["ship"]); !reflect.DeepEqual(lines, []string{"ship #1 (user) h"}) {
			t.Errorf("ship = %v", lines)
		}
	})

	t.Run("SDLC_USER_CONFIG names a missing file", func(t *testing.T) {
		root := instructionsRoot(t, "[[instructions.ship]]\ntext = \"t\"\n", "")
		t.Setenv(UserConfigPathEnv, filepath.Join(t.TempDir(), "missing.toml"))
		got := mustReadInstructions(t, root)
		if lines := instructionLines(got["ship"]); !reflect.DeepEqual(lines, []string{"ship #1 (team) t"}) {
			t.Errorf("ship = %v", lines)
		}
	})
}

func TestReadInstructions_RootsDiffer(t *testing.T) {
	mainRoot := instructionsRoot(t, "[[instructions.pr]]\ntext = \"main\"\n", "")
	// A linked worktree with its own .sdlc-v2/ files: never read.
	instructionsRoot(t, "[[instructions.pr]]\ntext = \"linked\"\n", "[[instructions.pr]]\ntext = \"linked local\"\n")

	got := mustReadInstructions(t, mainRoot)
	if lines := instructionLines(got["pr"]); !reflect.DeepEqual(lines, []string{"pr #1 (team) main"}) {
		t.Errorf("pr = %v, want only the main worktree files", lines)
	}
}

// TestReadInstructions_PathStates runs the folder and link rows through
// ReadInstructions and through ReadSection, the reader of
// loadHardenInstructions, and asserts the same error class.
func TestReadInstructions_PathStates(t *testing.T) {
	Quiet = true
	defer func() { Quiet = false }()

	t.Run("path is a folder", func(t *testing.T) {
		root := instructionsRoot(t, "[jira]\n", "")
		if err := os.MkdirAll(filepath.Join(root, ".sdlc-v2", "local.toml"), 0o755); err != nil {
			t.Fatal(err)
		}

		_, secErr := ReadSection(root, "review")
		if secErr == nil || errors.Is(secErr, ErrNotFound) {
			t.Fatalf("ReadSection error = %v, want a read failure (an InfraError at the harden tool)", secErr)
		}

		_, err := ReadInstructions(root)
		ie := mustInstructionsError(t, err)
		if ie.Kind != "infra" || ie.File != ".sdlc-v2/local.toml" || ie.Fix != "make the file readable." {
			t.Errorf("error = %+v, want Kind infra for .sdlc-v2/local.toml", ie)
		}
	})

	t.Run("path is a symbolic link", func(t *testing.T) {
		root := instructionsRoot(t, "[jira]\n", "")
		target := filepath.Join(t.TempDir(), "real-local.toml")
		writeInstructionsFile(t, target, "[review]\nthreshold = 1\n\n[[instructions.pr]]\ntext = \"linked\"\n")
		if err := os.Symlink(target, filepath.Join(root, ".sdlc-v2", "local.toml")); err != nil {
			t.Skipf("symlink: %v", err)
		}

		if _, err := ReadSection(root, "review"); err != nil {
			t.Fatalf("ReadSection through the link: %v", err)
		}
		got := mustReadInstructions(t, root)
		if lines := instructionLines(got["pr"]); !reflect.DeepEqual(lines, []string{"pr #1 (project local) linked"}) {
			t.Errorf("pr = %v", lines)
		}
	})
}

func TestInstructionsError_Error(t *testing.T) {
	e := &InstructionsError{Kind: "data", File: ".sdlc-v2/config.toml", Field: "instructions.pr[2].text", Problem: "the text is blank.", Fix: "set it."}
	want := "File: .sdlc-v2/config.toml\nField: instructions.pr[2].text\nProblem: the text is blank.\nFix: set it."
	if got := e.Error(); got != want {
		t.Errorf("Error() =\n%s\nwant\n%s", got, want)
	}
}

var instructionSchemaFiles = []string{"sdlc-config.schema.json", "sdlc-local.schema.json"}

// TestInstructionKeysMatchSchema proves that both schema enums equal
// InstructionKeys.
func TestInstructionKeysMatchSchema(t *testing.T) {
	for _, name := range instructionSchemaFiles {
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("..", "..", "plugins", "sdlc", "schemas", name))
			if err != nil {
				t.Fatal(err)
			}
			var schema struct {
				Defs struct {
					InstructionsSection struct {
						PropertyNames struct {
							Enum []string `json:"enum"`
						} `json:"propertyNames"`
					} `json:"instructionsSection"`
				} `json:"$defs"`
				Properties map[string]struct {
					Ref string `json:"$ref"`
				} `json:"properties"`
			}
			if err := json.Unmarshal(data, &schema); err != nil {
				t.Fatal(err)
			}
			if got := schema.Defs.InstructionsSection.PropertyNames.Enum; !slices.Equal(got, InstructionKeys) {
				t.Errorf("$defs.instructionsSection.propertyNames.enum = %v, want InstructionKeys %v", got, InstructionKeys)
			}
			if ref := schema.Properties["instructions"].Ref; ref != "#/$defs/instructionsSection" {
				t.Errorf("properties.instructions.$ref = %q, want #/$defs/instructionsSection", ref)
			}
		})
	}
}

// TestInstructionKeysCoverCanonicalSteps proves that InstructionKeys minus
// received-review, commit-fixes and ship equals shipmeta.CanonicalSteps, in
// order.
func TestInstructionKeysCoverCanonicalSteps(t *testing.T) {
	extra := []string{"received-review", "commit-fixes", "ship"}
	var steps []string
	for _, k := range InstructionKeys {
		if !slices.Contains(extra, k) {
			steps = append(steps, k)
		}
	}
	if !slices.Equal(steps, shipmeta.CanonicalSteps) {
		t.Errorf("InstructionKeys minus %v = %v, want shipmeta.CanonicalSteps %v", extra, steps, shipmeta.CanonicalSteps)
	}
	for _, k := range StandaloneInstructionKeys {
		if !slices.Contains(InstructionKeys, k) {
			t.Errorf("StandaloneInstructionKeys has %q, which is not in InstructionKeys", k)
		}
	}
}

// TestInstructionsSchema validates the instructions property of both
// schemas.
func TestInstructionsSchema(t *testing.T) {
	entries := func(key string, items ...map[string]any) map[string]any {
		list := make([]any, len(items))
		for i, it := range items {
			list[i] = it
		}
		return map[string]any{"instructions": map[string]any{key: list}}
	}
	many := func(n int) map[string]any {
		items := make([]map[string]any, n)
		for i := range items {
			items[i] = map[string]any{"text": "a"}
		}
		return entries("pr", items...)
	}

	cases := []struct {
		name    string
		doc     map[string]any
		wantErr bool
	}{
		{"empty section", map[string]any{"instructions": map[string]any{}}, false},
		{"empty list opt-out", map[string]any{"instructions": map[string]any{"harden": []any{}}}, false},
		{"string text with flags", entries("pr", map[string]any{"text": "a", "normalQuestions": true, "criticalQuestions": false, "onQuestionNotAllowed": "decide"}), false},
		{"text array", entries("ship", map[string]any{"text": []any{"a", "b"}}), false},
		{"text at cap", entries("pr", map[string]any{"text": strings.Repeat("a", MaxInstructionText)}), false},
		{"list at cap", many(MaxInstructionsPerKey), false},
		{"unknown key", entries("deploy", map[string]any{"text": "a"}), true},
		{"text missing", entries("pr", map[string]any{"normalQuestions": true}), true},
		{"text blank", entries("pr", map[string]any{"text": "  "}), true},
		{"text empty array", entries("pr", map[string]any{"text": []any{}}), true},
		{"text over cap", entries("pr", map[string]any{"text": strings.Repeat("a", MaxInstructionText+1)}), true},
		{"flag not bool", entries("pr", map[string]any{"text": "a", "normalQuestions": "yes"}), true},
		{"bad onQuestionNotAllowed", entries("pr", map[string]any{"text": "a", "onQuestionNotAllowed": "ask"}), true},
		{"unknown field", entries("pr", map[string]any{"text": "a", "color": "red"}), true},
		{"list over cap", many(MaxInstructionsPerKey + 1), true},
		{"key not a list", map[string]any{"instructions": map[string]any{"pr": "a"}}, true},
	}
	for _, name := range instructionSchemaFiles {
		abs, err := filepath.Abs(filepath.Join("..", "..", "plugins", "sdlc", "schemas", name))
		if err != nil {
			t.Fatal(err)
		}
		sch, err := jsonschema.NewCompiler().Compile(abs)
		if err != nil {
			t.Fatalf("compile %s: %v", name, err)
		}
		for _, tc := range cases {
			t.Run(name+"/"+tc.name, func(t *testing.T) {
				err := sch.Validate(tc.doc)
				if tc.wantErr && err == nil {
					t.Error("schema accepted a document it must reject")
				}
				if !tc.wantErr && err != nil {
					t.Errorf("schema rejected a valid document: %v", err)
				}
			})
		}
	}
}
