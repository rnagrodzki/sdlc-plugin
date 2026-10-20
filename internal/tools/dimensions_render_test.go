package tools

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rnagrodzki/sdlc-plugin/internal/dimensions"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
)

// ---------------------------------------------------------------------------
// dimensions_render_instructions: listDimensions mode tests
// ---------------------------------------------------------------------------

func TestDimensionsRender_ListDimensions_MissingDir(t *testing.T) {
	root := t.TempDir()

	out, err := dimensionsRenderInstructions(root, DimensionsRenderInstructionsIn{ListDimensions: true})
	if err != nil {
		t.Fatalf("dimensionsRenderInstructions: %v", err)
	}
	if !out.OK {
		t.Error("expected OK=true when review-dimensions/ does not exist yet")
	}
	if out.Dimensions == nil {
		t.Error("expected Dimensions to be a non-nil empty slice, got nil")
	}
	if len(out.Dimensions) != 0 {
		t.Errorf("expected 0 dimensions, got %v", out.Dimensions)
	}
	if out.Count != 0 {
		t.Errorf("expected Count=0, got %d", out.Count)
	}
	if out.Next == "" {
		t.Error("expected Next to be populated")
	}

	// The JSON encoding must show "[]", never "null" — omitempty is not set
	// on Dimensions, and a nil slice with omitempty absent would encode as
	// "null" if the field were ever left nil.
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !jsonHasEmptyArrayField(t, b, "dimensions") {
		t.Errorf(`expected "dimensions":[] in JSON, got %s`, string(b))
	}
}

// TestDimensionsRender_MirrorPathUsesTrimmedName pins that the mirror file
// name uses the same trimmed frontmatter name as the rendered heading. A
// quoted YAML name keeps its spaces, so this fails if the path is built from
// the raw value.
func TestDimensionsRender_MirrorPathUsesTrimmedName(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "dim.md"),
		"---\nname: \"  security \"\ndescription: Security review\ntriggers:\n  - \"**/*.go\"\n---\n# Security\n\nCheck input validation.\n")

	out, err := dimensionsRenderInstructions(root, DimensionsRenderInstructionsIn{File: "dim.md"})
	if err != nil {
		t.Fatalf("dimensionsRenderInstructions: %v", err)
	}
	want := filepath.Join(root, ".github", "instructions", "security.instructions.md")
	if out.Path != want {
		t.Errorf("Path = %q, want %q", out.Path, want)
	}
	if _, err := os.Stat(want); err != nil {
		t.Errorf("mirror file not written at the trimmed name: %v", err)
	}
}

// TestDimensionsRender_RenderWritesMirrorContent pins the mirror file that
// render mode writes, per the spec's "Render mode mirror content" table:
// applyTo from the triggers, the heading, the description, the default
// severity, the common text read from a relative commonFile, the Checklist
// with checkboxes made plain, the Severity Guide (ending at the next "## "
// heading), the skip-when Note, and the marker line at the end. An existing
// generated mirror is replaced whole. A commonFile that does not exist drops
// only the Common section.
func TestDimensionsRender_RenderWritesMirrorContent(t *testing.T) {
	const dimension = "---\n" +
		"name: security\n" +
		"description: \"  Security review for Go code.  \"\n" +
		"triggers:\n  - \"**/*.go\"\n  - \"cmd/**\"\n" +
		"skip-when:\n  - \"**/*_test.go\"\n  - \"docs/**\"\n" +
		"---\n" +
		"# Security\n\n" +
		"## Checklist\n\n- [ ] Validate all input.\n- [x] Escape output.\n\n" +
		"## Severity Guide\n\n- high: exploitable\n\n" +
		"## Examples\n\nNot part of the mirror.\n"
	const head = "---\n" +
		"applyTo: \"**/*.go,cmd/**\"\n" +
		"---\n" +
		"# security — Review Instructions\n\n" +
		"Security review for Go code.\n\n" +
		"Default severity: medium\n"
	const common = "\n## Common Review Instructions\n\n" +
		"Cite file and line for every finding.\n"
	const tail = "\n## Checklist\n\n- Validate all input.\n- Escape output.\n" +
		"\n## Severity Guide\n\n- high: exploitable\n" +
		"\n## Note\n\n" +
		"In Claude Code reviews, files matching these patterns are excluded: **/*_test.go, docs/**.\n" +
		"Copilot path-specific instructions do not support exclusion patterns — use judgment when findings apply to these files.\n"

	cases := []struct {
		name       string
		commonFile string
		want       string
	}{
		{"with common file", paths.DataDir + "/review-dimensions/_common.md", dimensions.StampMirror(head + common + tail)},
		{"common file missing", paths.DataDir + "/review-dimensions/absent.md", dimensions.StampMirror(head + tail)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeFile(t, filepath.Join(root, paths.DataDir, "review-dimensions", "security.md"), dimension)
			writeFile(t, filepath.Join(root, paths.DataDir, "review-dimensions", "_common.md"),
				"\n  Cite file and line for every finding.\n\n")
			mirror := filepath.Join(root, ".github", "instructions", "security.instructions.md")
			writeFile(t, mirror, dimensions.StampMirror("stale mirror content\n"))

			out, err := dimensionsRenderInstructions(root, DimensionsRenderInstructionsIn{
				File:       paths.DataDir + "/review-dimensions/security.md",
				CommonFile: tc.commonFile,
			})
			if err != nil {
				t.Fatalf("dimensionsRenderInstructions: %v", err)
			}
			if out.Path != mirror {
				t.Errorf("Path = %q, want %q", out.Path, mirror)
			}
			if want := "Rendered to " + mirror + "."; out.Next != want {
				t.Errorf("Next = %q, want %q", out.Next, want)
			}
			got, err := os.ReadFile(mirror)
			if err != nil {
				t.Fatalf("read mirror: %v", err)
			}
			if string(got) != tc.want {
				t.Errorf("mirror content mismatch\n--- got ---\n%s\n--- want ---\n%s", got, tc.want)
			}
		})
	}
}

func TestDimensionsRender_RenderPathTraversalName_Errors(t *testing.T) {
	for _, name := range []string{"../../escaped", "a/b", `a\\b`} {
		t.Run(name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "project")
			writeFile(t, filepath.Join(root, "dim.md"),
				"---\nname: \""+name+"\"\ndescription: Security review\ntriggers:\n  - \"**/*.go\"\n---\n# Security\n\nCheck input validation.\n")

			_, err := dimensionsRenderInstructions(root, DimensionsRenderInstructionsIn{File: "dim.md"})
			var de *mcpserver.DomainError
			if !errors.As(err, &de) {
				t.Fatalf("err = %v, want *mcpserver.DomainError", err)
			}
			if _, statErr := os.Stat(filepath.Join(filepath.Dir(root), "escaped.instructions.md")); statErr == nil {
				t.Error("mirror file was written outside .github/instructions/")
			}
		})
	}
}

func TestDimensionsRender_ListDimensions_UnreadableDirIsInfraError(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, paths.DataDir, "review-dimensions"), "not a directory")

	_, err := dimensionsRenderInstructions(root, DimensionsRenderInstructionsIn{ListDimensions: true})
	var infra *mcpserver.InfraError
	if !errors.As(err, &infra) {
		t.Fatalf("err = %v, want *mcpserver.InfraError", err)
	}
}

func TestDimensionsRender_ListDimensions_ExcludesCommonAndNonMarkdown(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, paths.DataDir, "review-dimensions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	files := map[string]string{
		"security.md": "---\nname: security\ntriggers: [\"**/*.go\"]\n---\nBody\n",
		"a11y.md":     "---\nname: a11y\ntriggers: [\"**/*.tsx\"]\n---\nBody\n",
		"_common.md":  "Shared instructions\n",
		"notes.txt":   "not a dimension file\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	out, err := dimensionsRenderInstructions(root, DimensionsRenderInstructionsIn{ListDimensions: true})
	if err != nil {
		t.Fatalf("dimensionsRenderInstructions: %v", err)
	}
	if !out.OK {
		t.Error("expected OK=true")
	}
	if out.Count != 2 {
		t.Errorf("expected Count=2, got %d (%v)", out.Count, out.Dimensions)
	}
	want := []string{"a11y", "security"}
	if len(out.Dimensions) != len(want) {
		t.Fatalf("expected %v, got %v", want, out.Dimensions)
	}
	for i, w := range want {
		if out.Dimensions[i] != w {
			t.Errorf("Dimensions[%d] = %q, want %q (full: %v)", i, out.Dimensions[i], w, out.Dimensions)
		}
	}
	if out.Path != dir {
		t.Errorf("expected Path=%q, got %q", dir, out.Path)
	}
}

// jsonHasEmptyArrayField reports whether the marshaled JSON contains the
// given field encoded as an empty array (never omitted, never "null").
func jsonHasEmptyArrayField(t *testing.T, b []byte, field string) bool {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	v, ok := m[field]
	if !ok {
		return false
	}
	arr, ok := v.([]any)
	return ok && len(arr) == 0
}

// ---------------------------------------------------------------------------
// dimensions_render_instructions: Next field coverage on existing modes
// ---------------------------------------------------------------------------

func TestDimensionsRender_WriteDimension_SetsNext(t *testing.T) {
	root := t.TempDir()

	out, err := dimensionsRenderInstructions(root, DimensionsRenderInstructionsIn{
		WriteDimension: true,
		Name:           "security",
		Content:        "---\nname: security\ntriggers: [\"**/*.go\"]\n---\nBody\n",
	})
	if err != nil {
		t.Fatalf("dimensionsRenderInstructions: %v", err)
	}
	if !out.OK {
		t.Error("expected OK=true")
	}
	if out.Next == "" {
		t.Error("expected Next to be populated on write mode")
	}
	if out.Dimensions == nil {
		t.Error("expected Dimensions to be a non-nil empty slice on write mode")
	}
}

func TestDimensionsRender_WriteDimension_EmptyName_Errors(t *testing.T) {
	root := t.TempDir()

	_, err := dimensionsRenderInstructions(root, DimensionsRenderInstructionsIn{
		WriteDimension: true,
		Content:        "body",
	})
	if err == nil {
		t.Fatal("expected error when name is empty")
	}
	if _, ok := err.(*mcpserver.DomainError); !ok {
		t.Errorf("expected *mcpserver.DomainError, got %T: %v", err, err)
	}
}

func TestDimensionsRender_WriteDimension_EmptyContent_Errors(t *testing.T) {
	root := t.TempDir()

	_, err := dimensionsRenderInstructions(root, DimensionsRenderInstructionsIn{
		WriteDimension: true,
		Name:           "security",
	})
	if err == nil {
		t.Fatal("expected error when content is empty")
	}
	if _, ok := err.(*mcpserver.DomainError); !ok {
		t.Errorf("expected *mcpserver.DomainError, got %T: %v", err, err)
	}
}

func TestDimensionsRender_WriteDimension_PathTraversalName_Errors(t *testing.T) {
	root := t.TempDir()

	for _, name := range []string{"../escape", "sub/dir", `back\slash`, ".."} {
		_, err := dimensionsRenderInstructions(root, DimensionsRenderInstructionsIn{
			WriteDimension: true,
			Name:           name,
			Content:        "body",
		})
		if err == nil {
			t.Errorf("name %q: expected error, got nil", name)
			continue
		}
		if _, ok := err.(*mcpserver.DomainError); !ok {
			t.Errorf("name %q: expected *mcpserver.DomainError, got %T: %v", name, err, err)
		}
	}

	// Confirm no file escaped review-dimensions/.
	if _, statErr := os.Stat(filepath.Join(root, "escape.md")); !os.IsNotExist(statErr) {
		t.Error("path-traversal name must not create a file outside review-dimensions/")
	}
}

func TestDimensionsRender_MultipleModes_Rejected(t *testing.T) {
	root := t.TempDir()

	_, err := dimensionsRenderInstructions(root, DimensionsRenderInstructionsIn{
		WriteDimension: true,
		ListDimensions: true,
		Name:           "security",
		Content:        "body",
	})
	if err == nil {
		t.Fatal("expected error when writeDimension and listDimensions are both true")
	}
	if _, ok := err.(*mcpserver.DomainError); !ok {
		t.Errorf("expected *mcpserver.DomainError, got %T: %v", err, err)
	}
}

// ---------------------------------------------------------------------------
// dimensions_render_instructions: marker line, refusal and overwrite
// ---------------------------------------------------------------------------

const mirrorGuardDimension = "---\nname: security\ndescription: Security review\ntriggers:\n  - \"**/*.go\"\n---\n# Security\n\n## Checklist\n\n- [ ] Validate all input.\n"

// mirrorGuardRoot returns a project root with a valid dimension file at
// dim.md and the path of the mirror that render mode writes for it.
func mirrorGuardRoot(t *testing.T) (root, mirror string) {
	t.Helper()
	root = t.TempDir()
	writeFile(t, filepath.Join(root, "dim.md"), mirrorGuardDimension)
	return root, filepath.Join(root, ".github", "instructions", "security.instructions.md")
}

func mirrorGuardRender(root string, overwrite bool) (DimensionsRenderInstructionsOut, error) {
	return dimensionsRenderInstructions(root, DimensionsRenderInstructionsIn{File: "dim.md", Overwrite: overwrite})
}

// mirrorGuardWant is the mirror that render mode writes for mirrorGuardDimension.
func mirrorGuardWant(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "dim.md"), mirrorGuardDimension)
	if _, err := mirrorGuardRender(root, false); err != nil {
		t.Fatalf("render fixture: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(root, ".github", "instructions", "security.instructions.md"))
	if err != nil {
		t.Fatal(err)
	}
	return string(got)
}

// TestDimensionsRender_AbsentMirror_WritesStampedMirror pins that a render to
// an absent mirror writes the rendered text ended with the marker line, and
// that a second render over it gives the same bytes.
func TestDimensionsRender_AbsentMirror_WritesStampedMirror(t *testing.T) {
	root, mirror := mirrorGuardRoot(t)

	out, err := mirrorGuardRender(root, false)
	if err != nil {
		t.Fatalf("first render: %v", err)
	}
	if want := "Rendered to " + mirror + "."; out.Next != want {
		t.Errorf("Next = %q, want %q", out.Next, want)
	}
	first, err := os.ReadFile(mirror)
	if err != nil {
		t.Fatal(err)
	}
	if got := dimensions.CheckMirror(first); got != dimensions.MirrorGenerated {
		t.Errorf("CheckMirror(first render) = %v, want MirrorGenerated", got)
	}
	lines := strings.Split(strings.TrimSuffix(string(first), "\n"), "\n")
	if last := lines[len(lines)-1]; !strings.HasPrefix(last, dimensions.MirrorMarkerPrefix) {
		t.Errorf("last line = %q, want a marker line", last)
	}
	if lines[0] != "---" || !strings.HasPrefix(lines[1], "applyTo:") || lines[2] != "---" {
		t.Errorf("lines 1 to 3 are not the Copilot frontmatter: %q", lines[:3])
	}

	if _, err := mirrorGuardRender(root, false); err != nil {
		t.Fatalf("second render: %v", err)
	}
	second, err := os.ReadFile(mirror)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Errorf("second render changed the bytes\n--- first ---\n%s\n--- second ---\n%s", first, second)
	}
}

// TestDimensionsRender_GeneratedMirror_IsReplaced pins that a mirror in state
// Generated is replaced, with LF and with CRLF line endings.
func TestDimensionsRender_GeneratedMirror_IsReplaced(t *testing.T) {
	want := mirrorGuardWant(t)
	generated := dimensions.StampMirror("old generated text\n")

	cases := []struct {
		name    string
		content string
	}{
		{"LF", generated},
		{"CRLF", strings.ReplaceAll(generated, "\n", "\r\n")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root, mirror := mirrorGuardRoot(t)
			writeFile(t, mirror, tc.content)

			if _, err := mirrorGuardRender(root, false); err != nil {
				t.Fatalf("render over a generated mirror: %v", err)
			}
			got, err := os.ReadFile(mirror)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != want {
				t.Errorf("mirror was not replaced\n--- got ---\n%s\n--- want ---\n%s", got, want)
			}
		})
	}
}

// mirrorGuardRefusalStates returns an existing mirror in each state that
// render mode refuses, with the reason text the refusal must carry.
func mirrorGuardRefusalStates() []struct {
	name    string
	content string
	reason  string
} {
	generated := dimensions.StampMirror("hand-checked text\n")
	return []struct {
		name    string
		content string
		reason  string
	}{
		{"no marker line", "hand-written rows\n", "no sdlc:generated marker line at the end of the file"},
		{"empty file", "", "no sdlc:generated marker line at the end of the file"},
		{"edited text", strings.Replace(generated, "hand-checked", "hand-edited", 1), "text changed after the last render"},
		{"bad hash", dimensions.MirrorMarkerPrefix + "XYZ" + dimensions.MirrorMarkerSuffix + "\n", "text changed after the last render"},
		{"over 1 MiB", strings.Repeat("x", maxMirrorBytes+1), "1048577 bytes, over the 1048576 byte limit"},
	}
}

// TestDimensionsRender_HandWrittenMirror_IsRefused pins that render mode
// returns the refusal DomainError, names the dimension file and overwrite in
// the Suggestion, and leaves the mirror bytes as they were.
func TestDimensionsRender_HandWrittenMirror_IsRefused(t *testing.T) {
	for _, tc := range mirrorGuardRefusalStates() {
		t.Run(tc.name, func(t *testing.T) {
			root, mirror := mirrorGuardRoot(t)
			writeFile(t, mirror, tc.content)

			_, err := mirrorGuardRender(root, false)
			var de *mcpserver.DomainError
			if !errors.As(err, &de) {
				t.Fatalf("err = %v, want *mcpserver.DomainError", err)
			}
			wantMsg := "has hand-written content (" + tc.reason + ") and was not replaced"
			if !strings.Contains(de.Msg, wantMsg) || !strings.Contains(de.Msg, mirror) {
				t.Errorf("Msg = %q, want it to name %q and contain %q", de.Msg, mirror, wantMsg)
			}
			if !strings.Contains(de.Suggestion, filepath.Join(root, "dim.md")) || !strings.Contains(de.Suggestion, "overwrite: true") {
				t.Errorf("Suggestion = %q, want it to name the dimension file and overwrite: true", de.Suggestion)
			}
			got, err := os.ReadFile(mirror)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tc.content {
				t.Errorf("a refused render changed the mirror bytes (len %d, was %d)", len(got), len(tc.content))
			}
		})
	}
}

// TestDimensionsRender_Overwrite_ReplacesMirrorInEveryState pins that
// overwrite:true replaces a mirror in each refusal state.
func TestDimensionsRender_Overwrite_ReplacesMirrorInEveryState(t *testing.T) {
	want := mirrorGuardWant(t)
	for _, tc := range mirrorGuardRefusalStates() {
		t.Run(tc.name, func(t *testing.T) {
			root, mirror := mirrorGuardRoot(t)
			writeFile(t, mirror, tc.content)

			if _, err := mirrorGuardRender(root, true); err != nil {
				t.Fatalf("render with overwrite: %v", err)
			}
			got, err := os.ReadFile(mirror)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != want {
				t.Errorf("mirror was not replaced\n--- got ---\n%s\n--- want ---\n%s", got, want)
			}
		})
	}
}

// TestDimensionsRender_Overwrite_NoEffectInWriteAndListModes pins that write
// and list modes ignore overwrite.
func TestDimensionsRender_Overwrite_NoEffectInWriteAndListModes(t *testing.T) {
	root := t.TempDir()
	content := "---\nname: security\ntriggers: [\"**/*.go\"]\n---\nBody\n"

	out, err := dimensionsRenderInstructions(root, DimensionsRenderInstructionsIn{
		WriteDimension: true, Overwrite: true, Name: "security", Content: content,
	})
	if err != nil {
		t.Fatalf("write mode with overwrite: %v", err)
	}
	got, err := os.ReadFile(out.Path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != content {
		t.Errorf("write mode wrote %q, want %q", got, content)
	}
	if _, err := os.Stat(filepath.Join(root, ".github")); !os.IsNotExist(err) {
		t.Errorf("write mode with overwrite touched .github: %v", err)
	}

	out, err = dimensionsRenderInstructions(root, DimensionsRenderInstructionsIn{ListDimensions: true, Overwrite: true})
	if err != nil {
		t.Fatalf("list mode with overwrite: %v", err)
	}
	if out.Count != 1 || len(out.Dimensions) != 1 || out.Dimensions[0] != "security" {
		t.Errorf("list mode result = %v (count %d), want [security]", out.Dimensions, out.Count)
	}
}

// TestToolErrorSites_DimensionsRenderMirrorCheck pins the errors of the read
// of the current mirror: the refusal is a DomainError, a stat or read error
// is an InfraError, each Suggestion names the next step, and no byte is
// written. overwrite:true skips the read.
func TestToolErrorSites_DimensionsRenderMirrorCheck(t *testing.T) {
	toolErrSiteRun(t, []toolErrSiteCase{
		{
			name:  "mirror has hand-written content",
			class: toolErrDomain,
			run: func(t *testing.T) ([]string, error) {
				root, mirror := mirrorGuardRoot(t)
				writeFile(t, mirror, "hand-written rows\n")
				_, err := mirrorGuardRender(root, false)
				return []string{"dim.md", "overwrite: true"}, err
			},
		},
		{
			name:  "mirror path is a folder",
			class: toolErrInfra,
			run: func(t *testing.T) ([]string, error) {
				root, mirror := mirrorGuardRoot(t)
				toolErrSiteDir(t, mirror)
				_, err := mirrorGuardRender(root, false)
				if err != nil && !strings.HasPrefix(err.Error(), "read mirror ") {
					t.Errorf("Msg = %q, want a prefix of \"read mirror \"", err.Error())
				}
				return []string{mirror, "Nothing was written"}, err
			},
		},
		{
			name:  "mirror directory is a regular file",
			class: toolErrInfra,
			run: func(t *testing.T) ([]string, error) {
				root, mirror := mirrorGuardRoot(t)
				toolErrSiteBlock(t, filepath.Join(root, ".github"))
				_, err := mirrorGuardRender(root, false)
				return []string{mirror, "Nothing was written"}, err
			},
		},
	})

	t.Run("overwrite skips the read of a folder", func(t *testing.T) {
		root, mirror := mirrorGuardRoot(t)
		toolErrSiteDir(t, mirror)
		_, err := mirrorGuardRender(root, true)
		var infra *mcpserver.InfraError
		if !errors.As(err, &infra) || !strings.HasPrefix(infra.Msg, "write ") {
			t.Fatalf("err = %v, want the *mcpserver.InfraError of the write", err)
		}
	})
}
