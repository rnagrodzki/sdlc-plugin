package hooks

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/rnagrodzki/sdlc-plugin/internal/dimensions"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
)

// These tests cover the config branch and the mirror branch of
// post-tool-validate. The helpers (triggerEvent, mustBlock, assertSilent,
// chdir, realPath, mustMkdirAll, mustWriteFile) live in gate_hooks_test.go and
// session_start_test.go.

// assertNote checks that out is a PostToolUse note that does not block: a
// hookSpecificOutput with the wanted context text and no decision key.
func assertNote(t *testing.T, out Output, want string) {
	t.Helper()
	payload, ok := out.JSON.(map[string]any)
	if !ok {
		t.Fatalf("JSON = %v (%T), want map[string]any", out.JSON, out.JSON)
	}
	if _, blocked := payload["decision"]; blocked {
		t.Errorf("payload has a decision key, want a note that does not block")
	}
	hso, _ := payload["hookSpecificOutput"].(map[string]any)
	ctx, _ := hso["additionalContext"].(string)
	if !strings.Contains(ctx, want) {
		t.Errorf("additionalContext = %q, want substring %q", ctx, want)
	}
}

// writeHookConfig writes content to <root>/.sdlc-v2/config.toml and returns
// the path of the file.
func writeHookConfig(t *testing.T, root, content string) string {
	t.Helper()
	mustMkdirAll(t, filepath.Join(root, paths.DataDir))
	configPath := filepath.Join(root, paths.DataDir, paths.ConfigFile)
	mustWriteFile(t, configPath, content)
	return configPath
}

// invalidExecuteGuardrail has no description, so the guardrails validator
// reports one finding for the execute section.
const invalidExecuteGuardrail = "[execute.guardrails.no-desc]\nseverity = \"error\"\n"

// TestPostToolValidate_Config_InvalidGuardrailBlocks blocks an edit of config.toml that leaves an invalid guardrail.
func TestPostToolValidate_Config_InvalidGuardrailBlocks(t *testing.T) {
	dir := realPath(t, t.TempDir())
	chdir(t, dir)
	configPath := writeHookConfig(t, dir, invalidExecuteGuardrail)

	out, err := postToolValidate(HookCtx{}, triggerEvent(configPath))
	mustBlock(t, out, err, "[execute.guardrails] no-desc: description is missing")
	mustBlock(t, out, err, configPath)
}

// TestPostToolValidate_Config_Silent stays silent for a config.toml edit that has no finding or no validator result.
func TestPostToolValidate_Config_Silent(t *testing.T) {
	cases := []struct {
		name   string
		config string
	}{
		{
			name: "valid guardrails in both sections",
			config: "[plan.guardrails.plan-rule]\ndescription = \"A valid plan guardrail.\"\n\n" +
				"[execute.guardrails.exec-rule]\ndescription = \"A valid execute guardrail.\"\nseverity = \"warning\"\n",
		},
		{name: "config with no guardrail table", config: "[jira]\nsite = \"example.atlassian.net\"\n"},
		{name: "config that is not valid TOML", config: "[[[not toml\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := realPath(t, t.TempDir())
			chdir(t, dir)
			configPath := writeHookConfig(t, dir, c.config)

			out, err := postToolValidate(HookCtx{}, triggerEvent(configPath))
			if err != nil {
				t.Fatal(err)
			}
			assertSilent(t, out)
		})
	}
}

// TestPostToolValidate_Config_UsesRootOfEditedFile proves the config branch
// reads the config.toml that was edited, not the one in the process folder:
// the cwd is a second folder with no config at all.
func TestPostToolValidate_Config_UsesRootOfEditedFile(t *testing.T) {
	project := realPath(t, t.TempDir())
	configPath := writeHookConfig(t, project, invalidExecuteGuardrail)

	other := realPath(t, t.TempDir())
	chdir(t, other)

	out, err := postToolValidate(HookCtx{}, triggerEvent(configPath))
	mustBlock(t, out, err, "[execute.guardrails] no-desc: description is missing")
}

// writeMirrorFixture writes a dimension file named name and a mirror for it
// under root, and returns the dimension file path and the mirror path.
// mirrorText is the full text of the mirror file.
func writeMirrorFixture(t *testing.T, root, name, mirrorText string) (dimFile, mirror string) {
	t.Helper()
	dimDir := filepath.Join(root, paths.DataDir, paths.ReviewDimensionsSubdir)
	mustMkdirAll(t, dimDir)
	dimFile = filepath.Join(dimDir, name+".md")
	mustWriteFile(t, dimFile, "---\nname: "+name+"\n---\n")

	instrDir := filepath.Join(root, ".github", "instructions")
	mustMkdirAll(t, instrDir)
	mirror = filepath.Join(instrDir, name+".instructions.md")
	mustWriteFile(t, mirror, mirrorText)
	return dimFile, mirror
}

// TestPostToolValidate_Mirror_NoteAfterHandEdit adds a note, and does not block, for a mirror with text changed by hand.
func TestPostToolValidate_Mirror_NoteAfterHandEdit(t *testing.T) {
	dir := realPath(t, t.TempDir())
	chdir(t, dir)

	// A stamped mirror with one byte changed above the marker line.
	edited := strings.Replace(dimensions.StampMirror("Check the security rules.\n"), "security", "Security", 1)
	dimFile, mirror := writeMirrorFixture(t, dir, "security", edited)

	out, err := postToolValidate(HookCtx{}, triggerEvent(mirror))
	if err != nil {
		t.Fatal(err)
	}
	assertNote(t, out, dimFile)
	assertNote(t, out, "overwrite: true")
}

// TestPostToolValidate_Mirror_NoteWhenNoMarkerLine adds a note for a mirror that has no marker line at all.
func TestPostToolValidate_Mirror_NoteWhenNoMarkerLine(t *testing.T) {
	dir := realPath(t, t.TempDir())
	chdir(t, dir)

	// A mirror from before the marker line existed: no marker line at all.
	dimFile, mirror := writeMirrorFixture(t, dir, "security", "Check the security rules.\n")

	out, err := postToolValidate(HookCtx{}, triggerEvent(mirror))
	if err != nil {
		t.Fatal(err)
	}
	assertNote(t, out, dimFile)
	assertNote(t, out, "overwrite: true")
}

// TestPostToolValidate_Mirror_UsesRootOfEditedFile finds the dimension file
// under the root of the edited mirror, not under the process folder.
func TestPostToolValidate_Mirror_UsesRootOfEditedFile(t *testing.T) {
	project := realPath(t, t.TempDir())
	dimFile, mirror := writeMirrorFixture(t, project, "security", "Check the security rules.\n")

	// The process folder holds no dimension file of that name.
	other := realPath(t, t.TempDir())
	chdir(t, other)

	out, err := postToolValidate(HookCtx{}, triggerEvent(mirror))
	if err != nil {
		t.Fatal(err)
	}
	assertNote(t, out, dimFile)
}

// TestPostToolValidate_Mirror_Silent stays silent for a mirror with a valid marker line, a mirror with no dimension file, and an unreadable path.
func TestPostToolValidate_Mirror_Silent(t *testing.T) {
	t.Run("valid marker line", func(t *testing.T) {
		dir := realPath(t, t.TempDir())
		chdir(t, dir)
		_, mirror := writeMirrorFixture(t, dir, "security", dimensions.StampMirror("Check the security rules.\n"))

		out, err := postToolValidate(HookCtx{}, triggerEvent(mirror))
		if err != nil {
			t.Fatal(err)
		}
		assertSilent(t, out)
	})

	t.Run("no marker line and no dimension file", func(t *testing.T) {
		dir := realPath(t, t.TempDir())
		chdir(t, dir)
		instrDir := filepath.Join(dir, ".github", "instructions")
		mustMkdirAll(t, instrDir)
		mirror := filepath.Join(instrDir, "hand-written.instructions.md")
		mustWriteFile(t, mirror, "Rules written by hand.\n")

		out, err := postToolValidate(HookCtx{}, triggerEvent(mirror))
		if err != nil {
			t.Fatal(err)
		}
		assertSilent(t, out)
	})

	t.Run("path with no file", func(t *testing.T) {
		dir := realPath(t, t.TempDir())
		chdir(t, dir)
		// A dimension file of the same name exists, so only the read error
		// can make the output silent.
		dimDir := filepath.Join(dir, paths.DataDir, paths.ReviewDimensionsSubdir)
		mustMkdirAll(t, dimDir)
		mustWriteFile(t, filepath.Join(dimDir, "security.md"), "---\nname: security\n---\n")

		out, err := postToolValidate(HookCtx{}, triggerEvent(filepath.Join(dir, ".github", "instructions", "security.instructions.md")))
		if err != nil {
			t.Fatal(err)
		}
		assertSilent(t, out)
	})
}
